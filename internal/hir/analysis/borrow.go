package analysis

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/hir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
)

type placeSegmentKind int

const (
	segmentField placeSegmentKind = iota
	segmentIndex
)

type placeSegment struct {
	kind placeSegmentKind
	name string
}

type borrowEntry struct {
	path    []placeSegment
	mutable bool
	loc     *source.Location
}

type borrowBinding struct {
	place   borrowPlace
	mutable bool
	loc     *source.Location
}

type borrowRecord struct {
	place   borrowPlace
	mutable bool
	loc     *source.Location
}

type borrowPlace struct {
	base *symbols.Symbol
	path []placeSegment
}

type borrowScope struct {
	refs    map[*symbols.Symbol]struct{}
	lastUse map[*symbols.Symbol]lastUseInfo
}

type borrowChecker struct {
	ctx       *context_v2.CompilerContext
	mod       *context_v2.Module
	borrows   map[*symbols.Symbol][]borrowEntry
	bindings  map[*symbols.Symbol]borrowBinding
	moved     map[*symbols.Symbol]*source.Location
	scopes    []borrowScope
	temp      []borrowRecord
	locals    map[*symbols.Symbol]struct{}
	localRefs map[*symbols.Symbol]localRefUse
}

type lastUseInfo struct {
	index int
	loc   *source.Location
}

type localRefUse struct {
	base *symbols.Symbol
	loc  *source.Location
}

func checkBorrowRules(ctx *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	if hirMod == nil {
		return
	}
	for _, node := range hirMod.Items {
		switch decl := node.(type) {
		case *hir.FuncDecl:
			checker := newBorrowChecker(ctx, mod)
			checker.checkFuncDecl(decl)
		case *hir.MethodDecl:
			checker := newBorrowChecker(ctx, mod)
			checker.checkMethodDecl(decl)
		}
	}
}

func newBorrowChecker(ctx *context_v2.CompilerContext, mod *context_v2.Module) *borrowChecker {
	return &borrowChecker{
		ctx:       ctx,
		mod:       mod,
		borrows:   make(map[*symbols.Symbol][]borrowEntry),
		bindings:  make(map[*symbols.Symbol]borrowBinding),
		moved:     make(map[*symbols.Symbol]*source.Location),
		locals:    make(map[*symbols.Symbol]struct{}),
		localRefs: make(map[*symbols.Symbol]localRefUse),
	}
}

func (b *borrowChecker) checkFuncDecl(decl *hir.FuncDecl) {
	if decl == nil || decl.Body == nil {
		return
	}
	b.checkBlock(decl.Body)
}

func (b *borrowChecker) checkMethodDecl(decl *hir.MethodDecl) {
	if decl == nil || decl.Body == nil {
		return
	}
	b.checkBlock(decl.Body)
}

func (b *borrowChecker) pushScope(lastUse map[*symbols.Symbol]lastUseInfo) {
	b.scopes = append(b.scopes, borrowScope{
		refs:    make(map[*symbols.Symbol]struct{}),
		lastUse: lastUse,
	})
}

func (b *borrowChecker) popScope() {
	if len(b.scopes) == 0 {
		return
	}
	scope := b.scopes[len(b.scopes)-1]
	b.scopes = b.scopes[:len(b.scopes)-1]
	for ref := range scope.refs {
		b.releaseBinding(ref)
	}
}

func (b *borrowChecker) withTempScope(fn func()) {
	start := len(b.temp)
	fn()
	b.releaseTemps(start)
}

func (b *borrowChecker) releaseTemps(start int) {
	for i := len(b.temp) - 1; i >= start; i-- {
		rec := b.temp[i]
		b.releaseBorrow(rec.place, rec.mutable, rec.loc)
	}
	b.temp = b.temp[:start]
}

func (b *borrowChecker) checkBlock(block *hir.Block) {
	if block == nil {
		return
	}
	refDecls := collectRefDecls(block)
	lastUse := computeLastUse(block, refDecls)
	b.pushScope(lastUse)
	for idx, node := range block.Nodes {
		b.checkNode(node)
		b.releaseExpiredRefs(idx)
	}
	b.popScope()
}

func (b *borrowChecker) releaseExpiredRefs(idx int) {
	if len(b.scopes) == 0 {
		return
	}
	scope := &b.scopes[len(b.scopes)-1]
	if scope.lastUse == nil {
		return
	}
	var expired []*symbols.Symbol
	for sym := range scope.refs {
		last, ok := scope.lastUse[sym]
		if ok && last.index <= idx {
			expired = append(expired, sym)
		}
	}
	for _, sym := range expired {
		b.releaseBinding(sym)
		delete(scope.refs, sym)
	}
}

func (b *borrowChecker) checkNode(node hir.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.Block:
		b.checkBlock(n)
	case *hir.VarDecl:
		b.checkVarDecl(n)
	case *hir.ConstDecl:
		b.checkVarDecl(&hir.VarDecl{Decls: n.Decls, Location: n.Location})
	case *hir.DeclStmt:
		b.checkNode(n.Decl)
	case *hir.AssignStmt:
		b.checkAssignStmt(n)
	case *hir.ReturnStmt:
		b.checkReturnStmt(n)
	case *hir.ExprStmt:
		b.withTempScope(func() {
			b.checkExpr(n.X)
		})
	case *hir.IfStmt:
		b.checkIfStmt(n)
	case *hir.ForStmt:
		b.checkForStmt(n)
	case *hir.WhileStmt:
		b.checkWhileStmt(n)
	case *hir.MatchStmt:
		b.checkMatchStmt(n)
	case *hir.DeferStmt:
		b.withTempScope(func() {
			b.checkExpr(n.Call)
		})
	}
}

func (b *borrowChecker) checkVarDecl(stmt *hir.VarDecl) {
	if stmt == nil {
		return
	}

	for _, item := range stmt.Decls {
		if item.Name != nil && item.Name.Symbol != nil {
			b.locals[item.Name.Symbol] = struct{}{}
		}
		declType := declItemType(item)
		isRefDecl := isReferenceType(declType)

		if item.Value == nil {
			continue
		}

		if isRefDecl {
			if borrowExpr, ok := item.Value.(*hir.UnaryExpr); ok && isBorrowOp(borrowExpr.Op.Kind) {
				b.checkBorrowInit(item.Name, borrowExpr)
				b.updateLocalRefSymbol(item.Name, borrowExpr, true)
				continue
			}
			if ident, ok := item.Value.(*hir.Ident); ok {
				b.checkExpr(ident)
				b.bindRefFromIdent(item.Name, ident, true)
				b.updateLocalRefSymbol(item.Name, ident, true)
				continue
			}
		}

		b.withTempScope(func() {
			b.checkExpr(item.Value)
		})
		b.updateLocalRefSymbol(item.Name, item.Value, true)
	}
}

func (b *borrowChecker) checkAssignStmt(stmt *hir.AssignStmt) {
	if stmt == nil {
		return
	}

	if stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN {
		if ident := directIdent(stmt.Lhs); ident != nil && isReferenceSymbol(ident.Symbol) {
			b.checkRefRebind(ident, stmt.Rhs)
			return
		}
	}

	start := len(b.temp)
	b.checkExpr(stmt.Rhs)
	if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
		b.checkWriteTargetWithMode(stmt.Lhs, false)
	} else {
		b.checkWriteTargetWithMode(stmt.Lhs, true)
	}
	b.releaseTemps(start)
	allowClear := stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN
	b.updateLocalRefForAssign(stmt.Lhs, stmt.Rhs, allowClear)
}

func (b *borrowChecker) checkReturnStmt(stmt *hir.ReturnStmt) {
	if stmt == nil || stmt.Result == nil {
		return
	}

	b.withTempScope(func() {
		b.checkExpr(stmt.Result)
	})
	b.checkReturnLifetime(stmt)
	b.checkReturnMove(stmt.Result)
}

func (b *borrowChecker) checkIfStmt(stmt *hir.IfStmt) {
	if stmt == nil {
		return
	}

	b.withTempScope(func() {
		b.checkExpr(stmt.Cond)
	})
	if stmt.Body != nil {
		b.checkBlock(stmt.Body)
	}
	if stmt.Else != nil {
		b.checkNode(stmt.Else)
	}
}

func (b *borrowChecker) checkForStmt(stmt *hir.ForStmt) {
	if stmt == nil {
		return
	}

	b.pushScope(nil)
	if stmt.Iterator != nil {
		b.checkNode(stmt.Iterator)
	}
	b.withTempScope(func() {
		b.checkExpr(stmt.Range)
	})
	if stmt.Body != nil {
		b.checkBlock(stmt.Body)
	}
	b.popScope()
}

func (b *borrowChecker) checkWhileStmt(stmt *hir.WhileStmt) {
	if stmt == nil {
		return
	}

	b.withTempScope(func() {
		b.checkExpr(stmt.Cond)
	})
	if stmt.Body != nil {
		b.checkBlock(stmt.Body)
	}
}

func (b *borrowChecker) checkMatchStmt(stmt *hir.MatchStmt) {
	if stmt == nil {
		return
	}

	b.withTempScope(func() {
		b.checkExpr(stmt.Expr)
	})
	for _, clause := range stmt.Cases {
		if clause.Pattern != nil {
			b.withTempScope(func() {
				b.checkExpr(clause.Pattern)
			})
		}
		if clause.Body != nil {
			b.checkBlock(clause.Body)
		}
	}
}

func (b *borrowChecker) checkExpr(expr hir.Expr) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *hir.Ident:
		if e.Symbol != nil && isReferenceSymbol(e.Symbol) {
			b.checkRefValueUse(e)
			return
		}
		b.checkRead(e)
	case *hir.Literal:
		return
	case *hir.OptionalNone:
		return
	case *hir.OptionalSome:
		b.checkExpr(e.Value)
	case *hir.OptionalIsSome:
		b.checkExpr(e.Value)
	case *hir.OptionalUnwrap:
		b.checkExpr(e.Value)
		if e.Default != nil {
			b.checkExpr(e.Default)
		}
	case *hir.OptionalIsNone:
		b.checkExpr(e.Value)
	case *hir.ResultOk:
		b.checkExpr(e.Value)
	case *hir.ResultErr:
		b.checkExpr(e.Value)
	case *hir.ResultUnwrap:
		b.checkExpr(e.Value)
		b.checkCatchClause(e.Catch)
	case *hir.BinaryExpr:
		b.checkExpr(e.X)
		b.checkExpr(e.Y)
	case *hir.UnaryExpr:
		if isBorrowOp(e.Op.Kind) {
			b.checkBorrowExpr(e)
			return
		}
		if isMoveOp(e.Op.Kind) {
			b.checkMoveExpr(e)
			return
		}
		b.checkExpr(e.X)
	case *hir.DerefExpr:
		// Dereference requires read access to the referenced place.
		if ident, ok := e.X.(*hir.Ident); ok && isReferenceSymbol(ident.Symbol) {
			// Avoid treating the reference value itself as a write access when dereferencing.
		} else {
			b.checkExpr(e.X)
		}
		place, via := b.borrowAccessPlace(e)
		if place.base != nil {
			b.checkAccess(place, accessRead, e.Loc(), via)
		}
	case *hir.PrefixExpr:
		if isIncDecOp(e.Op.Kind) {
			b.checkWriteTargetWithMode(e.X, false)
			return
		}
		b.checkExpr(e.X)
	case *hir.PostfixExpr:
		if isIncDecOp(e.Op.Kind) {
			b.checkWriteTargetWithMode(e.X, false)
			return
		}
		b.checkExpr(e.X)
	case *hir.CallExpr:
		b.checkMethodCall(e)
		for _, arg := range e.Args {
			b.checkExpr(arg)
		}
		b.checkCatchClause(e.Catch)
	case *hir.IndexExpr:
		place, via := b.borrowAccessPlace(e)
		if place.base != nil {
			b.checkAccess(place, accessRead, e.Loc(), via)
		}
		b.checkAddressableExpr(e.X, place.base)
		b.checkExpr(e.Index)
	case *hir.CastExpr:
		b.checkExpr(e.X)
	case *hir.CoalescingExpr:
		b.checkExpr(e.Cond)
		b.checkExpr(e.Default)
	case *hir.CompositeLit:
		for _, elt := range e.Elts {
			b.checkExpr(elt)
		}
	case *hir.ArrayLenExpr:
		b.checkExpr(e.X)
	case *hir.StringLenExpr:
		b.checkExpr(e.X)
	case *hir.MapIterInitExpr:
		b.checkExpr(e.Map)
	case *hir.MapIterNextExpr:
		b.checkExpr(e.Map)
		b.checkExpr(e.Iter)
	case *hir.SelectorExpr:
		place, via := b.borrowAccessPlace(e)
		if place.base != nil {
			b.checkAccess(place, accessRead, e.Loc(), via)
		}
		b.checkAddressableExpr(e.X, place.base)
	case *hir.RangeExpr:
		b.checkExpr(e.Start)
		b.checkExpr(e.End)
		b.checkExpr(e.Incr)
	case *hir.ForkExpr:
		b.checkExpr(e.Call)
	case *hir.ScopeResolutionExpr:
		place, via := b.borrowAccessPlace(e)
		if place.base != nil {
			b.checkAccess(place, accessRead, e.Loc(), via)
		}
	case *hir.ParenExpr:
		b.checkExpr(e.X)
	case *hir.KeyValueExpr:
		b.checkExpr(e.Key)
		b.checkExpr(e.Value)
	case *hir.FuncLit:
		if e.Body != nil {
			nested := newBorrowChecker(b.ctx, b.mod)
			nested.checkBlock(e.Body)
		}
	}
}

func (b *borrowChecker) checkMethodCall(call *hir.CallExpr) {
	if call == nil || call.Fun == nil {
		b.checkExpr(call.Fun)
		return
	}

	// Check if this is a method call (Fun is a SelectorExpr)
	selector, ok := call.Fun.(*hir.SelectorExpr)
	if !ok {
		b.checkExpr(call.Fun)
		return
	}

	// Get method info by looking up the method in the receiver type
	if b.requiresMutableReceiver(selector) {
		// Method needs &mut receiver, check if we can get mutable borrow
		place, via := b.borrowAccessPlace(selector.X)
		if place.base != nil {
			if !b.checkNotMoved(place.base, selector.Loc()) {
				b.checkExpr(call.Fun)
				return
			}
			// Check if we can get a mutable borrow (don't actually add it yet)
			entries := b.borrows[place.base]
			if entry, ok := b.findBorrow(entries, place.base, place.path, nil, via); ok {
				releaseLoc := b.borrowReleaseLoc(place.base, entry)
				b.reportBorrowError(selector.Loc(), fmt.Sprintf("cannot borrow '%s' because it is still actively borrowed", place.base.Name), entry.loc, releaseLoc)
				return
			}
		}
	}

	// Now check the function expression normally
	b.checkExpr(call.Fun)
}

func (b *borrowChecker) requiresMutableReceiver(selector *hir.SelectorExpr) bool {
	if selector == nil || selector.Field == nil {
		return false
	}

	baseType := exprType(selector.X) // typename
	if baseType == nil {
		return false
	}

	baseType = types.DereferenceType(baseType)

	named, ok := baseType.(*types.NamedType)
	if !ok || named.Name == "" {
		return false
	}

	// Look up type symbol using existing module scope pattern
	var typeSym *symbols.Symbol
	if sym, found := b.mod.ModuleScope.Lookup(named.Name); found && sym.Kind == symbols.SymbolType {
		typeSym = sym
	} else {
		// Search imported modules
		for _, importPath := range b.mod.ImportAliasMap {
			if importedMod, exists := b.ctx.GetModule(importPath); exists {
				if sym, ok := importedMod.ModuleScope.GetSymbol(named.Name); ok && sym.Kind == symbols.SymbolType {
					typeSym = sym
					break
				}
			}
		}
	}

	if typeSym == nil || typeSym.Methods == nil {
		return false
	}

	method, ok := typeSym.Methods[selector.Field.Name]
	if !ok || method == nil || method.Receiver == nil {
		return false
	}

	refType, ok := types.UnwrapType(method.Receiver).(*types.ReferenceType)
	return ok && refType.Mutable
}

func (b *borrowChecker) checkCatchClause(clause *hir.CatchClause) {
	if clause == nil {
		return
	}
	if clause.Handler != nil {
		b.checkBlock(clause.Handler)
	}
	if clause.Fallback != nil {
		b.withTempScope(func() {
			b.checkExpr(clause.Fallback)
		})
	}
}

func (b *borrowChecker) checkRead(ident *hir.Ident) {
	if ident == nil || ident.Symbol == nil {
		return
	}
	if isReferenceSymbol(ident.Symbol) {
		return
	}
	b.checkAccess(borrowPlace{base: ident.Symbol}, accessRead, ident.Loc(), nil)
}

func (b *borrowChecker) checkRefValueUse(ident *hir.Ident) {
	if ident == nil || ident.Symbol == nil {
		return
	}
	binding, ok := b.bindings[ident.Symbol]
	place := borrowPlace{base: ident.Symbol}
	if ok && binding.place.base != nil {
		place = binding.place
	}
	kind := accessRead
	if refType, ok := types.UnwrapType(ident.Symbol.Type).(*types.ReferenceType); ok && refType.Mutable {
		kind = accessWrite
	}
	b.checkAccess(place, kind, ident.Loc(), ident.Symbol)
}

func directIdent(expr hir.Expr) *hir.Ident {
	switch e := expr.(type) {
	case *hir.Ident:
		return e
	case *hir.ParenExpr:
		return directIdent(e.X)
	default:
		return nil
	}
}

func (b *borrowChecker) checkMoveExpr(expr *hir.UnaryExpr) {
	if expr == nil {
		return
	}
	ident, ok := expr.X.(*hir.Ident)
	if !ok || ident.Symbol == nil {
		b.checkExpr(expr.X)
		return
	}
	if isReferenceSymbol(ident.Symbol) {
		b.checkRefValueUse(ident)
		return
	}
	if !b.checkNotMoved(ident.Symbol, expr.Loc()) {
		return
	}
	entries := b.borrows[ident.Symbol]
	if entry, ok := b.findBorrow(entries, ident.Symbol, nil, nil, nil); ok {
		releaseLoc := b.borrowReleaseLoc(ident.Symbol, entry)
		diag := diagnostics.NewError(fmt.Sprintf("cannot move '%s' because it is borrowed", ident.Symbol.Name)).
			WithCode(diagnostics.ErrInvalidOperation)
		if expr.Loc() != nil {
			diag = diag.WithPrimaryLabel(expr.Loc(), "move here")
		}
		if entry.loc != nil {
			diag = diag.WithSecondaryLabel(entry.loc, "borrowed here")
		}
		if releaseLoc != nil && releaseLoc != entry.loc {
			diag = diag.WithSecondaryLabel(releaseLoc, "borrow ends here")
		}
		b.ctx.Diagnostics.Add(diag)
		return
	}
	b.markMoved(ident.Symbol, expr.Loc())
}

func (b *borrowChecker) markMoved(sym *symbols.Symbol, loc *source.Location) {
	if sym == nil {
		return
	}
	b.moved[sym] = loc
}

func (b *borrowChecker) clearMoved(sym *symbols.Symbol) {
	if sym == nil {
		return
	}
	delete(b.moved, sym)
}

func (b *borrowChecker) checkNotMoved(sym *symbols.Symbol, loc *source.Location) bool {
	if sym == nil {
		return true
	}
	movedLoc, ok := b.moved[sym]
	if !ok {
		return true
	}
	diag := diagnostics.NewError(fmt.Sprintf("use of moved value '%s'", sym.Name)).
		WithCode(diagnostics.ErrInvalidOperation)
	if loc != nil {
		diag = diag.WithPrimaryLabel(loc, "moved value used here")
	}
	if movedLoc != nil {
		diag = diag.WithSecondaryLabel(movedLoc, "value moved here")
	}
	b.ctx.Diagnostics.Add(diag)
	return false
}

func (b *borrowChecker) checkWriteTargetWithMode(expr hir.Expr, allowReinit bool) {
	place, via := b.borrowAccessPlace(expr)
	if place.base == nil || (isReferenceSymbol(place.base) && via == nil) {
		b.checkAddressableExpr(expr, place.base)
		return
	}
	if allowReinit {
		if ident := directIdent(expr); ident != nil && ident.Symbol != nil && place.base == ident.Symbol && len(place.path) == 0 && via == nil {
			b.clearMoved(ident.Symbol)
		}
	}
	b.checkAccess(place, accessWrite, expr.Loc(), via)
	b.checkAddressableExpr(expr, place.base)
}

func (b *borrowChecker) checkBorrowInit(name *hir.Ident, expr *hir.UnaryExpr) {
	b.bindRefBorrow(name, expr, true)
}

func (b *borrowChecker) bindRefBorrow(name *hir.Ident, expr *hir.UnaryExpr, registerScope bool) {
	if expr == nil {
		return
	}
	place, via := b.borrowAccessPlace(expr.X)
	mutable := expr.Op.Kind == tokens.MUT_TOKEN
	if place.base != nil && !b.checkNotMoved(place.base, expr.Loc()) {
		b.checkAddressableExpr(expr.X, place.base)
		return
	}
	if place.base != nil {
		if b.addBorrow(place, mutable, expr.Loc(), via) && name != nil && name.Symbol != nil {
			b.bindings[name.Symbol] = borrowBinding{place: place, mutable: mutable, loc: expr.Loc()}
			if registerScope && len(b.scopes) > 0 {
				b.scopes[len(b.scopes)-1].refs[name.Symbol] = struct{}{}
			}
		}
	}
	b.checkAddressableExpr(expr.X, place.base)
}

func (b *borrowChecker) bindRefFromIdent(name *hir.Ident, value *hir.Ident, registerScope bool) {
	if name == nil || name.Symbol == nil || value == nil || value.Symbol == nil {
		return
	}
	binding, ok := b.bindings[value.Symbol]
	if !ok || binding.place.base == nil {
		return
	}
	if b.addBorrow(binding.place, binding.mutable, value.Loc(), nil) {
		b.bindings[name.Symbol] = borrowBinding{place: binding.place, mutable: binding.mutable, loc: value.Loc()}
		if registerScope && len(b.scopes) > 0 {
			b.scopes[len(b.scopes)-1].refs[name.Symbol] = struct{}{}
		}
	}
}

func (b *borrowChecker) checkRefRebind(target *hir.Ident, rhs hir.Expr) {
	if target == nil || target.Symbol == nil {
		return
	}
	if borrowExpr, ok := rhs.(*hir.UnaryExpr); ok && isBorrowOp(borrowExpr.Op.Kind) {
		place, via := b.borrowAccessPlace(borrowExpr.X)
		mutable := borrowExpr.Op.Kind == tokens.MUT_TOKEN
		if place.base != nil && !b.checkNotMoved(place.base, borrowExpr.Loc()) {
			b.checkAddressableExpr(borrowExpr.X, place.base)
			return
		}
		b.releaseBinding(target.Symbol)
		if place.base != nil {
			if b.addBorrow(place, mutable, borrowExpr.Loc(), via) {
				b.bindings[target.Symbol] = borrowBinding{place: place, mutable: mutable, loc: borrowExpr.Loc()}
			}
		}
		b.checkAddressableExpr(borrowExpr.X, place.base)
		b.updateLocalRefSymbol(target, borrowExpr, true)
		return
	}
	if rhsIdent := directIdent(rhs); rhsIdent != nil {
		start := len(b.temp)
		b.checkExpr(rhsIdent)
		b.releaseTemps(start)
		if rhsIdent.Symbol == target.Symbol {
			return
		}
		b.releaseBinding(target.Symbol)
		b.bindRefFromIdent(target, rhsIdent, false)
		b.updateLocalRefSymbol(target, rhsIdent, true)
		return
	}

	start := len(b.temp)
	b.checkExpr(rhs)
	b.releaseTemps(start)
	b.releaseBinding(target.Symbol)
	b.updateLocalRefSymbol(target, rhs, true)
}

func (b *borrowChecker) checkBorrowExpr(expr *hir.UnaryExpr) {
	if expr == nil {
		return
	}
	place, via := b.borrowAccessPlace(expr.X)
	mutable := expr.Op.Kind == tokens.MUT_TOKEN
	if place.base != nil && !b.checkNotMoved(place.base, expr.Loc()) {
		b.checkAddressableExpr(expr.X, place.base)
		return
	}
	if place.base != nil {
		if b.addBorrow(place, mutable, expr.Loc(), via) {
			b.temp = append(b.temp, borrowRecord{place: place, mutable: mutable, loc: expr.Loc()})
		}
	}
	b.checkAddressableExpr(expr.X, place.base)
}

func (b *borrowChecker) borrowAccessPlace(expr hir.Expr) (borrowPlace, *symbols.Symbol) {
	switch e := expr.(type) {
	case *hir.Ident:
		return borrowPlace{base: e.Symbol}, nil
	case *hir.DerefExpr:
		place, via := b.borrowAccessPlace(e.X)
		return b.liftRefPlace(place, via)
	case *hir.SelectorExpr:
		place, via := b.borrowAccessPlace(e.X)
		place, via = b.liftRefPlace(place, via)
		if place.base == nil {
			return place, via
		}
		if e.Field == nil {
			return place, via
		}
		return place.withField(e.Field.Name), via
	case *hir.IndexExpr:
		place, via := b.borrowAccessPlace(e.X)
		place, via = b.liftRefPlace(place, via)
		if place.base == nil {
			return place, via
		}
		return place.withIndex(), via
	case *hir.ParenExpr:
		return b.borrowAccessPlace(e.X)
	case *hir.ScopeResolutionExpr:
		if sym := b.resolveScopeResolutionSymbol(e); sym != nil {
			return borrowPlace{base: sym}, nil
		}
		return borrowPlace{}, nil
	default:
		return borrowPlace{}, nil
	}
}

func (b *borrowChecker) liftRefPlace(place borrowPlace, via *symbols.Symbol) (borrowPlace, *symbols.Symbol) {
	if place.base == nil || !isReferenceSymbol(place.base) {
		return place, via
	}
	refSym := place.base
	if via == nil {
		via = refSym
	}
	binding, ok := b.bindings[refSym]
	if !ok || binding.place.base == nil {
		return place, via
	}
	lifted := binding.place
	for _, seg := range place.path {
		if seg.kind == segmentField {
			lifted = lifted.withField(seg.name)
		} else {
			lifted = lifted.withIndex()
		}
	}
	return lifted, via
}

func (p borrowPlace) withField(name string) borrowPlace {
	next := clonePath(p.path)
	next = append(next, placeSegment{kind: segmentField, name: name})
	return borrowPlace{base: p.base, path: next}
}

func (p borrowPlace) withIndex() borrowPlace {
	next := clonePath(p.path)
	next = append(next, placeSegment{kind: segmentIndex})
	return borrowPlace{base: p.base, path: next}
}

func clonePath(path []placeSegment) []placeSegment {
	if len(path) == 0 {
		return nil
	}
	out := make([]placeSegment, len(path))
	copy(out, path)
	return out
}

func (b *borrowChecker) checkAddressableExpr(expr hir.Expr, base *symbols.Symbol) {
	switch e := expr.(type) {
	case *hir.Ident:
		if e.Symbol == nil || e.Symbol != base {
			b.checkRead(e)
		}
	case *hir.SelectorExpr:
		b.checkAddressableExpr(e.X, base)
	case *hir.IndexExpr:
		b.checkAddressableExpr(e.X, base)
		b.checkExpr(e.Index)
	case *hir.ParenExpr:
		b.checkAddressableExpr(e.X, base)
	case *hir.ScopeResolutionExpr:
		if sym := b.resolveScopeResolutionSymbol(e); sym != nil && sym != base {
			b.checkAccess(borrowPlace{base: sym}, accessRead, e.Loc(), nil)
		}
	default:
		b.checkExpr(expr)
	}
}

func (b *borrowChecker) resolveScopeResolutionSymbol(expr *hir.ScopeResolutionExpr) *symbols.Symbol {
	if b == nil || b.ctx == nil || b.mod == nil || expr == nil || expr.Selector == nil {
		return nil
	}
	ident, ok := expr.X.(*hir.Ident)
	if !ok || ident == nil {
		return nil
	}
	leftName := ident.Name
	rightName := expr.Selector.Name
	if leftName == "" || rightName == "" {
		return nil
	}
	if b.mod.CurrentScope != nil {
		if typeSym, ok := b.mod.CurrentScope.Lookup(leftName); ok && typeSym.Kind == symbols.SymbolType {
			return nil
		}
	}
	importPath, ok := b.mod.ImportAliasMap[leftName]
	if !ok {
		return nil
	}
	importedMod, exists := b.ctx.GetModule(importPath)
	if !exists {
		return nil
	}
	sym, ok := importedMod.ModuleScope.GetSymbol(rightName)
	if !ok {
		return nil
	}
	return sym
}

type accessKind int

const (
	accessRead accessKind = iota
	accessWrite
)

func (b *borrowChecker) checkAccess(place borrowPlace, kind accessKind, loc *source.Location, via *symbols.Symbol) {
	if place.base == nil {
		return
	}
	if !b.checkNotMoved(place.base, loc) {
		return
	}
	entries := b.borrows[place.base]
	if len(entries) == 0 {
		return
	}
	switch kind {
	case accessRead:
		wantMutable := true
		if entry, ok := b.findBorrow(entries, place.base, place.path, &wantMutable, via); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot access '%s' while it is mutably borrowed", place.base.Name), entry.loc, releaseLoc)
		}
	case accessWrite:
		wantMutable := true
		if entry, ok := b.findBorrow(entries, place.base, place.path, &wantMutable, via); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot modify '%s' while it is mutably borrowed", place.base.Name), entry.loc, releaseLoc)
			return
		}
		wantShared := false
		if entry, ok := b.findBorrow(entries, place.base, place.path, &wantShared, via); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot modify '%s' while it is immutably borrowed", place.base.Name), entry.loc, releaseLoc)
		}
	}
}

func (b *borrowChecker) addBorrow(place borrowPlace, mutable bool, loc *source.Location, via *symbols.Symbol) bool {
	if place.base == nil {
		return true
	}
	entries := b.borrows[place.base]
	if mutable {
		if entry, ok := b.findBorrow(entries, place.base, place.path, nil, via); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			if entry.mutable {
				b.reportBorrowError(loc, fmt.Sprintf("cannot borrow '%s' as mutable because it is already mutably borrowed", place.base.Name), entry.loc, releaseLoc)
			} else {
				b.reportBorrowError(loc, fmt.Sprintf("cannot borrow '%s' as mutable because it is also borrowed as immutable", place.base.Name), entry.loc, releaseLoc)
			}
			return false
		}
	} else {
		wantMutable := true
		if entry, ok := b.findBorrow(entries, place.base, place.path, &wantMutable, via); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot borrow '%s' as immutable because it is already mutably borrowed", place.base.Name), entry.loc, releaseLoc)
			return false
		}
	}

	entry := borrowEntry{path: clonePath(place.path), mutable: mutable, loc: loc}
	b.borrows[place.base] = append(entries, entry)
	return true
}

func (b *borrowChecker) releaseBorrow(place borrowPlace, mutable bool, loc *source.Location) {
	if place.base == nil {
		return
	}
	entries := b.borrows[place.base]
	if len(entries) == 0 {
		return
	}
	entries = removeBorrowEntry(entries, place.path, mutable, loc)
	if len(entries) == 0 {
		delete(b.borrows, place.base)
		return
	}
	b.borrows[place.base] = entries
}

func (b *borrowChecker) releaseBinding(sym *symbols.Symbol) {
	if sym == nil {
		return
	}
	binding, ok := b.bindings[sym]
	if !ok {
		return
	}
	delete(b.bindings, sym)
	b.releaseBorrow(binding.place, binding.mutable, binding.loc)
}

func (b *borrowChecker) borrowReleaseLoc(base *symbols.Symbol, entry borrowEntry) *source.Location {
	if base == nil {
		return nil
	}
	sym := b.findBindingSymbol(base, entry)
	if sym == nil {
		return nil
	}
	return b.lastUseLoc(sym)
}

func (b *borrowChecker) findBindingSymbol(base *symbols.Symbol, entry borrowEntry) *symbols.Symbol {
	for sym, binding := range b.bindings {
		if binding.place.base != base {
			continue
		}
		if binding.mutable != entry.mutable {
			continue
		}
		if !pathsEqual(binding.place.path, entry.path) {
			continue
		}
		if entry.loc != nil && binding.loc != nil && binding.loc != entry.loc {
			continue
		}
		return sym
	}
	return nil
}

func (b *borrowChecker) lastUseLoc(sym *symbols.Symbol) *source.Location {
	if sym == nil {
		return nil
	}
	for i := len(b.scopes) - 1; i >= 0; i-- {
		scope := &b.scopes[i]
		if scope.lastUse == nil {
			continue
		}
		if info, ok := scope.lastUse[sym]; ok {
			return info.loc
		}
	}
	return nil
}

func (b *borrowChecker) checkReturnLifetime(retStmt *hir.ReturnStmt) {
	if retStmt == nil {
		return
	}
	if !typeContainsReference(exprType(retStmt.Result)) {
		return
	}
	use, ok := b.localRefInExpr(retStmt.Result)
	if !ok || use.base == nil {
		return
	}
	diag := diagnostics.NewError(fmt.Sprintf("cannot return value containing reference to local '%s'", use.base.Name)).
		WithCode(diagnostics.ErrInvalidOperation)
	if retStmt.Loc() != nil {
		diag = diag.WithPrimaryLabel(&source.Location{Start: retStmt.Loc().Start, End: retStmt.Loc().Start, Filename: retStmt.Loc().Filename}, "reference escapes here")
	}
	if use.loc != nil {
		diag = diag.WithSecondaryLabel(use.loc, "borrowed here")
	}
	b.ctx.Diagnostics.Add(diag)
}

func (b *borrowChecker) checkReturnMove(expr hir.Expr) {
	if expr == nil {
		return
	}
	expr = unwrapParenExpr(expr)
	unary, ok := expr.(*hir.UnaryExpr)
	if !ok || !isMoveOp(unary.Op.Kind) {
		return
	}
	ident, ok := unary.X.(*hir.Ident)
	if !ok || ident.Symbol == nil {
		return
	}
	if _, ok := b.locals[ident.Symbol]; !ok {
		return
	}
	if ident.Symbol.IsHeap {
		return
	}
	diag := diagnostics.NewError(fmt.Sprintf("cannot return moved local '%s'", ident.Symbol.Name)).
		WithCode(diagnostics.ErrInvalidOperation)
	if expr.Loc() != nil {
		diag = diag.WithPrimaryLabel(expr.Loc(), "move returned here")
	}
	diag = diag.WithHelp("allocate the value on the heap with '#' before moving it")
	b.ctx.Diagnostics.Add(diag)
}

func unwrapParenExpr(expr hir.Expr) hir.Expr {
	for {
		if p, ok := expr.(*hir.ParenExpr); ok {
			expr = p.X
			continue
		}
		return expr
	}
}

func (b *borrowChecker) reportBorrowError(loc *source.Location, msg string, borrowLoc *source.Location, releaseLoc *source.Location) {
	diag := diagnostics.NewError(msg).WithCode(diagnostics.ErrInvalidOperation)
	if loc != nil {
		diag = diag.WithPrimaryLabel(loc, "borrow here")
	}
	if borrowLoc != nil {
		diag = diag.WithSecondaryLabel(borrowLoc, "first borrowed here")
	}
	if releaseLoc != nil && releaseLoc != borrowLoc {
		diag = diag.WithSecondaryLabel(releaseLoc, "borrow ends here")
	}
	b.ctx.Diagnostics.Add(diag)
}

func declItemType(item hir.DeclItem) types.SemType {
	if item.Type != nil {
		return item.Type
	}
	if item.Name != nil && item.Name.Type != nil {
		return item.Name.Type
	}
	if item.Value != nil {
		return exprType(item.Value)
	}
	return nil
}

func collectRefDecls(block *hir.Block) map[*symbols.Symbol]struct{} {
	refs := make(map[*symbols.Symbol]struct{})
	if block == nil {
		return refs
	}
	for _, node := range block.Nodes {
		switch n := node.(type) {
		case *hir.VarDecl:
			addRefDecls(n.Decls, refs)
		case *hir.ConstDecl:
			addRefDecls(n.Decls, refs)
		case *hir.DeclStmt:
			if decl, ok := n.Decl.(*hir.VarDecl); ok {
				addRefDecls(decl.Decls, refs)
			} else if decl, ok := n.Decl.(*hir.ConstDecl); ok {
				addRefDecls(decl.Decls, refs)
			}
		}
	}
	return refs
}

func addRefDecls(items []hir.DeclItem, refs map[*symbols.Symbol]struct{}) {
	for _, item := range items {
		if item.Name == nil || item.Name.Symbol == nil {
			continue
		}
		if isReferenceType(declItemType(item)) {
			refs[item.Name.Symbol] = struct{}{}
		}
	}
}

func computeLastUse(block *hir.Block, refs map[*symbols.Symbol]struct{}) map[*symbols.Symbol]lastUseInfo {
	last := make(map[*symbols.Symbol]lastUseInfo, len(refs))
	if block == nil || len(refs) == 0 {
		return last
	}
	for idx, node := range block.Nodes {
		markRefDeclIndex(node, refs, last, idx)
		uses := make(map[*symbols.Symbol]*source.Location)
		collectRefUsesNode(node, refs, uses)
		for sym, loc := range uses {
			last[sym] = lastUseInfo{index: idx, loc: loc}
		}
	}
	return last
}

func markRefDeclIndex(node hir.Node, refs map[*symbols.Symbol]struct{}, last map[*symbols.Symbol]lastUseInfo, idx int) {
	switch n := node.(type) {
	case *hir.VarDecl:
		markDeclItems(n.Decls, refs, last, idx)
	case *hir.ConstDecl:
		markDeclItems(n.Decls, refs, last, idx)
	case *hir.DeclStmt:
		if decl, ok := n.Decl.(*hir.VarDecl); ok {
			markDeclItems(decl.Decls, refs, last, idx)
		} else if decl, ok := n.Decl.(*hir.ConstDecl); ok {
			markDeclItems(decl.Decls, refs, last, idx)
		}
	}
}

func markDeclItems(items []hir.DeclItem, refs map[*symbols.Symbol]struct{}, last map[*symbols.Symbol]lastUseInfo, idx int) {
	for _, item := range items {
		if item.Name == nil || item.Name.Symbol == nil {
			continue
		}
		if _, ok := refs[item.Name.Symbol]; !ok {
			continue
		}
		if _, seen := last[item.Name.Symbol]; !seen {
			last[item.Name.Symbol] = lastUseInfo{index: idx, loc: item.Name.Loc()}
		}
	}
}

func collectRefUsesNode(node hir.Node, refs map[*symbols.Symbol]struct{}, uses map[*symbols.Symbol]*source.Location) {
	if node == nil || len(refs) == 0 {
		return
	}
	switch n := node.(type) {
	case *hir.Block:
		for _, child := range n.Nodes {
			collectRefUsesNode(child, refs, uses)
		}
	case *hir.VarDecl:
		for _, item := range n.Decls {
			if item.Value != nil {
				collectRefUsesExpr(item.Value, refs, uses)
			}
		}
	case *hir.ConstDecl:
		for _, item := range n.Decls {
			if item.Value != nil {
				collectRefUsesExpr(item.Value, refs, uses)
			}
		}
	case *hir.DeclStmt:
		collectRefUsesNode(n.Decl, refs, uses)
	case *hir.AssignStmt:
		collectRefUsesExpr(n.Lhs, refs, uses)
		collectRefUsesExpr(n.Rhs, refs, uses)
	case *hir.ReturnStmt:
		collectRefUsesExpr(n.Result, refs, uses)
	case *hir.ExprStmt:
		collectRefUsesExpr(n.X, refs, uses)
	case *hir.IfStmt:
		collectRefUsesExpr(n.Cond, refs, uses)
		collectRefUsesNode(n.Body, refs, uses)
		collectRefUsesNode(n.Else, refs, uses)
	case *hir.ForStmt:
		collectRefUsesNode(n.Iterator, refs, uses)
		collectRefUsesExpr(n.Range, refs, uses)
		collectRefUsesNode(n.Body, refs, uses)
	case *hir.WhileStmt:
		collectRefUsesExpr(n.Cond, refs, uses)
		collectRefUsesNode(n.Body, refs, uses)
	case *hir.MatchStmt:
		collectRefUsesExpr(n.Expr, refs, uses)
		for _, clause := range n.Cases {
			collectRefUsesExpr(clause.Pattern, refs, uses)
			collectRefUsesNode(clause.Body, refs, uses)
		}
	case *hir.DeferStmt:
		collectRefUsesExpr(n.Call, refs, uses)
	case hir.Expr:
		collectRefUsesExpr(n, refs, uses)
	}
}

func collectRefUsesExpr(expr hir.Expr, refs map[*symbols.Symbol]struct{}, uses map[*symbols.Symbol]*source.Location) {
	if expr == nil || len(refs) == 0 {
		return
	}
	switch e := expr.(type) {
	case *hir.Ident:
		if e.Symbol != nil {
			if _, ok := refs[e.Symbol]; ok {
				uses[e.Symbol] = e.Loc()
			}
		}
	case *hir.OptionalSome:
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.OptionalIsSome:
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.OptionalIsNone:
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.OptionalUnwrap:
		collectRefUsesExpr(e.Value, refs, uses)
		collectRefUsesExpr(e.Default, refs, uses)
	case *hir.ResultOk:
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.ResultErr:
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.ResultUnwrap:
		collectRefUsesExpr(e.Value, refs, uses)
		if e.Catch != nil {
			collectRefUsesNode(e.Catch.Handler, refs, uses)
			collectRefUsesExpr(e.Catch.Fallback, refs, uses)
		}
	case *hir.BinaryExpr:
		collectRefUsesExpr(e.X, refs, uses)
		collectRefUsesExpr(e.Y, refs, uses)
	case *hir.UnaryExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.DerefExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.PrefixExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.PostfixExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.CallExpr:
		collectRefUsesExpr(e.Fun, refs, uses)
		for _, arg := range e.Args {
			collectRefUsesExpr(arg, refs, uses)
		}
		if e.Catch != nil {
			collectRefUsesNode(e.Catch.Handler, refs, uses)
			collectRefUsesExpr(e.Catch.Fallback, refs, uses)
		}
	case *hir.IndexExpr:
		collectRefUsesExpr(e.X, refs, uses)
		collectRefUsesExpr(e.Index, refs, uses)
	case *hir.CastExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.CoalescingExpr:
		collectRefUsesExpr(e.Cond, refs, uses)
		collectRefUsesExpr(e.Default, refs, uses)
	case *hir.CompositeLit:
		for _, elt := range e.Elts {
			collectRefUsesExpr(elt, refs, uses)
		}
	case *hir.ArrayLenExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.StringLenExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.MapIterInitExpr:
		collectRefUsesExpr(e.Map, refs, uses)
	case *hir.MapIterNextExpr:
		collectRefUsesExpr(e.Map, refs, uses)
		collectRefUsesExpr(e.Iter, refs, uses)
		if e.Key != nil && e.Key.Symbol != nil {
			if _, ok := refs[e.Key.Symbol]; ok {
				uses[e.Key.Symbol] = e.Key.Loc()
			}
		}
		if e.Value != nil && e.Value.Symbol != nil {
			if _, ok := refs[e.Value.Symbol]; ok {
				uses[e.Value.Symbol] = e.Value.Loc()
			}
		}
	case *hir.SelectorExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.RangeExpr:
		collectRefUsesExpr(e.Start, refs, uses)
		collectRefUsesExpr(e.End, refs, uses)
		collectRefUsesExpr(e.Incr, refs, uses)
	case *hir.ForkExpr:
		collectRefUsesExpr(e.Call, refs, uses)
	case *hir.ScopeResolutionExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.ParenExpr:
		collectRefUsesExpr(e.X, refs, uses)
	case *hir.KeyValueExpr:
		collectRefUsesExpr(e.Key, refs, uses)
		collectRefUsesExpr(e.Value, refs, uses)
	case *hir.FuncLit:
		for _, cap := range e.Captures {
			if cap != nil && cap.Symbol != nil {
				if _, ok := refs[cap.Symbol]; ok {
					uses[cap.Symbol] = cap.Loc()
				}
			}
		}
		collectRefUsesNode(e.Body, refs, uses)
	}
}

func (b *borrowChecker) findBorrow(entries []borrowEntry, base *symbols.Symbol, path []placeSegment, wantMutable *bool, ignore *symbols.Symbol) (borrowEntry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !pathsOverlap(path, entry.path) {
			continue
		}
		if wantMutable != nil && entry.mutable != *wantMutable {
			continue
		}
		if ignore != nil && base != nil {
			if sym := b.findBindingSymbol(base, entry); sym == ignore {
				continue
			}
		}
		return entry, true
	}
	return borrowEntry{}, false
}

func removeBorrowEntry(entries []borrowEntry, path []placeSegment, mutable bool, loc *source.Location) []borrowEntry {
	if len(entries) == 0 {
		return entries
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.mutable != mutable {
			continue
		}
		if !pathsEqual(entry.path, path) {
			continue
		}
		if loc != nil && entry.loc != loc {
			continue
		}
		copy(entries[i:], entries[i+1:])
		return entries[:len(entries)-1]
	}
	return entries
}

func pathsOverlap(a, b []placeSegment) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		left := a[i]
		right := b[i]
		if left.kind == segmentIndex || right.kind == segmentIndex {
			return true
		}
		if left.kind != right.kind || left.name != right.name {
			return false
		}
	}
	return true
}

func pathsEqual(a, b []placeSegment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind || a[i].name != b[i].name {
			return false
		}
	}
	return true
}

func isBorrowOp(kind tokens.TOKEN) bool {
	return kind == tokens.BIT_AND_TOKEN || kind == tokens.MUT_TOKEN
}

func isMoveOp(kind tokens.TOKEN) bool {
	return kind == tokens.AT_TOKEN
}

func isIncDecOp(kind tokens.TOKEN) bool {
	return kind == tokens.PLUS_PLUS_TOKEN || kind == tokens.MINUS_MINUS_TOKEN
}

func isReferenceType(t types.SemType) bool {
	if t == nil {
		return false
	}
	_, ok := types.UnwrapType(t).(*types.ReferenceType)
	return ok
}

func isReferenceSymbol(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	return isReferenceType(sym.Type)
}

func (b *borrowChecker) updateLocalRefSymbol(name *hir.Ident, expr hir.Expr, allowClear bool) {
	if name == nil || name.Symbol == nil || expr == nil {
		return
	}
	use, ok := b.localRefInExpr(expr)
	if ok {
		b.localRefs[name.Symbol] = use
		return
	}
	if allowClear {
		delete(b.localRefs, name.Symbol)
	}
}

func (b *borrowChecker) updateLocalRefForAssign(lhs hir.Expr, rhs hir.Expr, allowClear bool) {
	if lhs == nil || rhs == nil {
		return
	}
	use, ok := b.localRefInExpr(rhs)
	if ident := directIdent(lhs); ident != nil && ident.Symbol != nil {
		if ok {
			b.localRefs[ident.Symbol] = use
			return
		}
		if allowClear {
			delete(b.localRefs, ident.Symbol)
		}
		return
	}
	if !ok {
		return
	}
	place, _ := b.borrowAccessPlace(lhs)
	if place.base == nil {
		return
	}
	b.localRefs[place.base] = use
}

func (b *borrowChecker) localRefInExpr(expr hir.Expr) (localRefUse, bool) {
	if expr == nil {
		return localRefUse{}, false
	}
	if kv, ok := expr.(*hir.KeyValueExpr); ok {
		if use, ok := b.localRefInExpr(kv.Key); ok {
			return use, true
		}
		return b.localRefInExpr(kv.Value)
	}
	if !typeContainsReference(exprType(expr)) {
		return localRefUse{}, false
	}

	switch e := expr.(type) {
	case *hir.Ident:
		return b.localRefFromIdent(e)
	case *hir.UnaryExpr:
		if isBorrowOp(e.Op.Kind) {
			return b.localRefFromBorrow(e)
		}
		if isMoveOp(e.Op.Kind) {
			return b.localRefInExpr(e.X)
		}
		return b.localRefInExpr(e.X)
	case *hir.DerefExpr:
		return b.localRefInExpr(e.X)
	case *hir.OptionalNone:
		return localRefUse{}, false
	case *hir.OptionalSome:
		return b.localRefInExpr(e.Value)
	case *hir.OptionalUnwrap:
		if use, ok := b.localRefInExpr(e.Value); ok {
			return use, true
		}
		return b.localRefInExpr(e.Default)
	case *hir.ResultOk:
		return b.localRefInExpr(e.Value)
	case *hir.ResultErr:
		return b.localRefInExpr(e.Value)
	case *hir.ResultUnwrap:
		if use, ok := b.localRefInExpr(e.Value); ok {
			return use, true
		}
		if e.Catch != nil {
			if use, ok := b.localRefInExpr(e.Catch.Fallback); ok {
				return use, true
			}
		}
		return localRefUse{}, false
	case *hir.BinaryExpr:
		if use, ok := b.localRefInExpr(e.X); ok {
			return use, true
		}
		return b.localRefInExpr(e.Y)
	case *hir.CoalescingExpr:
		if use, ok := b.localRefInExpr(e.Cond); ok {
			return use, true
		}
		return b.localRefInExpr(e.Default)
	case *hir.CastExpr:
		return b.localRefInExpr(e.X)
	case *hir.CallExpr:
		for _, arg := range e.Args {
			if use, ok := b.localRefInExpr(arg); ok {
				return use, true
			}
		}
		return localRefUse{}, false
	case *hir.IndexExpr:
		return b.localRefInExpr(e.X)
	case *hir.SelectorExpr:
		return b.localRefInExpr(e.X)
	case *hir.CompositeLit:
		for _, elt := range e.Elts {
			if use, ok := b.localRefInExpr(elt); ok {
				return use, true
			}
		}
		return localRefUse{}, false
	case *hir.ArrayLenExpr:
		return b.localRefInExpr(e.X)
	case *hir.StringLenExpr:
		return b.localRefInExpr(e.X)
	case *hir.MapIterInitExpr:
		return b.localRefInExpr(e.Map)
	case *hir.MapIterNextExpr:
		if use, ok := b.localRefInExpr(e.Map); ok {
			return use, true
		}
		return b.localRefInExpr(e.Iter)
	case *hir.ScopeResolutionExpr:
		return b.localRefInExpr(e.X)
	case *hir.RangeExpr:
		if use, ok := b.localRefInExpr(e.Start); ok {
			return use, true
		}
		if use, ok := b.localRefInExpr(e.End); ok {
			return use, true
		}
		return b.localRefInExpr(e.Incr)
	case *hir.ParenExpr:
		return b.localRefInExpr(e.X)
	}

	return localRefUse{}, false
}

func (b *borrowChecker) localRefFromIdent(ident *hir.Ident) (localRefUse, bool) {
	if ident == nil || ident.Symbol == nil {
		return localRefUse{}, false
	}
	if use, ok := b.localRefs[ident.Symbol]; ok {
		return use, true
	}
	if isReferenceSymbol(ident.Symbol) {
		if binding, ok := b.bindings[ident.Symbol]; ok && binding.place.base != nil {
			if b.isLocalBorrowBase(binding.place.base) {
				return localRefUse{base: binding.place.base, loc: binding.loc}, true
			}
		}
	}
	return localRefUse{}, false
}

func (b *borrowChecker) localRefFromBorrow(expr *hir.UnaryExpr) (localRefUse, bool) {
	if expr == nil {
		return localRefUse{}, false
	}
	place, _ := b.borrowAccessPlace(expr.X)
	if place.base == nil {
		return localRefUse{}, false
	}
	if b.isLocalBorrowBase(place.base) {
		return localRefUse{base: place.base, loc: expr.Loc()}, true
	}
	return localRefUse{}, false
}

func (b *borrowChecker) isLocalBorrowBase(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	if _, ok := b.locals[sym]; ok {
		return true
	}
	switch sym.Kind {
	case symbols.SymbolParameter, symbols.SymbolReceiver:
		return !isReferenceType(sym.Type)
	default:
		return false
	}
}

func typeContainsReference(t types.SemType) bool {
	seen := make(map[types.SemType]bool)
	return typeContainsReferenceHelper(t, seen)
}

func typeContainsReferenceHelper(t types.SemType, seen map[types.SemType]bool) bool {
	if t == nil {
		return false
	}
	t = types.UnwrapType(t)

	// Check if we've already visited this type (cycle detection)
	if seen[t] {
		return false
	}
	seen[t] = true

	switch tt := t.(type) {
	case *types.ReferenceType:
		return true
	case *types.OptionalType:
		return typeContainsReferenceHelper(tt.Inner, seen)
	case *types.ResultType:
		return typeContainsReferenceHelper(tt.Ok, seen) || typeContainsReferenceHelper(tt.Err, seen)
	case *types.ArrayType:
		return typeContainsReferenceHelper(tt.Element, seen)
	case *types.MapType:
		return typeContainsReferenceHelper(tt.Key, seen) || typeContainsReferenceHelper(tt.Value, seen)
	case *types.StructType:
		for _, field := range tt.Fields {
			if typeContainsReferenceHelper(field.Type, seen) {
				return true
			}
		}
		return false
	case *types.UnionType:
		for _, variant := range tt.Variants {
			if typeContainsReferenceHelper(variant, seen) {
				return true
			}
		}
		return false
	case *types.EnumType:
		for _, variant := range tt.Variants {
			if variant.Type != nil && typeContainsReferenceHelper(variant.Type, seen) {
				return true
			}
		}
		return false
	case *types.InterfaceType:
		return true
	default:
		return false
	}
}
