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
	ctx      *context_v2.CompilerContext
	mod      *context_v2.Module
	borrows  map[*symbols.Symbol][]borrowEntry
	bindings map[*symbols.Symbol]borrowBinding
	scopes   []borrowScope
	temp     []borrowRecord
	locals   map[*symbols.Symbol]struct{}
}

type lastUseInfo struct {
	index int
	loc   *source.Location
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
		ctx:      ctx,
		mod:      mod,
		borrows:  make(map[*symbols.Symbol][]borrowEntry),
		bindings: make(map[*symbols.Symbol]borrowBinding),
		locals:   make(map[*symbols.Symbol]struct{}),
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
				continue
			}
			if ident, ok := item.Value.(*hir.Ident); ok {
				b.checkExpr(ident)
				b.bindRefFromIdent(item.Name, ident)
				continue
			}
		}

		b.withTempScope(func() {
			b.checkExpr(item.Value)
		})
	}
}

func (b *borrowChecker) checkAssignStmt(stmt *hir.AssignStmt) {
	if stmt == nil {
		return
	}

	start := len(b.temp)
	b.checkExpr(stmt.Rhs)
	b.checkWriteTarget(stmt.Lhs)
	b.releaseTemps(start)
}

func (b *borrowChecker) checkReturnStmt(stmt *hir.ReturnStmt) {
	if stmt == nil || stmt.Result == nil {
		return
	}

	b.withTempScope(func() {
		b.checkExpr(stmt.Result)
	})
	b.checkReturnLifetime(stmt.Result)
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
		b.checkExpr(e.X)
	case *hir.PrefixExpr:
		if isIncDecOp(e.Op.Kind) {
			b.checkWriteTarget(e.X)
			return
		}
		b.checkExpr(e.X)
	case *hir.PostfixExpr:
		if isIncDecOp(e.Op.Kind) {
			b.checkWriteTarget(e.X)
			return
		}
		b.checkExpr(e.X)
	case *hir.CallExpr:
		b.checkExpr(e.Fun)
		for _, arg := range e.Args {
			b.checkExpr(arg)
		}
		b.checkCatchClause(e.Catch)
	case *hir.IndexExpr:
		place := b.borrowPlace(e)
		if place.base != nil && !isReferenceSymbol(place.base) {
			b.checkAccess(place, accessRead, e.Loc())
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
		place := b.borrowPlace(e)
		if place.base != nil && !isReferenceSymbol(place.base) {
			b.checkAccess(place, accessRead, e.Loc())
		}
		b.checkAddressableExpr(e.X, place.base)
	case *hir.RangeExpr:
		b.checkExpr(e.Start)
		b.checkExpr(e.End)
		b.checkExpr(e.Incr)
	case *hir.ForkExpr:
		b.checkExpr(e.Call)
	case *hir.ScopeResolutionExpr:
		b.checkExpr(e.X)
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
	b.checkAccess(borrowPlace{base: ident.Symbol}, accessRead, ident.Loc())
}

func (b *borrowChecker) checkWriteTarget(expr hir.Expr) {
	place := b.borrowPlace(expr)
	if place.base == nil || isReferenceSymbol(place.base) {
		b.checkAddressableExpr(expr, place.base)
		return
	}
	b.checkAccess(place, accessWrite, expr.Loc())
	b.checkAddressableExpr(expr, place.base)
}

func (b *borrowChecker) checkBorrowInit(name *hir.Ident, expr *hir.UnaryExpr) {
	if expr == nil {
		return
	}
	place := b.borrowPlace(expr.X)
	mutable := expr.Op.Kind == tokens.MUT_REF_TOKEN
	if place.base != nil && !isReferenceSymbol(place.base) {
		if b.addBorrow(place, mutable, expr.Loc()) && name != nil && name.Symbol != nil {
			b.bindings[name.Symbol] = borrowBinding{place: place, mutable: mutable, loc: expr.Loc()}
			if len(b.scopes) > 0 {
				b.scopes[len(b.scopes)-1].refs[name.Symbol] = struct{}{}
			}
		}
	}
	b.checkAddressableExpr(expr.X, place.base)
}

func (b *borrowChecker) bindRefFromIdent(name *hir.Ident, value *hir.Ident) {
	if name == nil || name.Symbol == nil || value == nil || value.Symbol == nil {
		return
	}
	binding, ok := b.bindings[value.Symbol]
	if !ok || binding.place.base == nil {
		return
	}
	if b.addBorrow(binding.place, binding.mutable, value.Loc()) {
		b.bindings[name.Symbol] = borrowBinding{place: binding.place, mutable: binding.mutable, loc: value.Loc()}
		if len(b.scopes) > 0 {
			b.scopes[len(b.scopes)-1].refs[name.Symbol] = struct{}{}
		}
	}
}

func (b *borrowChecker) checkBorrowExpr(expr *hir.UnaryExpr) {
	if expr == nil {
		return
	}
	place := b.borrowPlace(expr.X)
	mutable := expr.Op.Kind == tokens.MUT_REF_TOKEN
	if place.base != nil && !isReferenceSymbol(place.base) {
		if b.addBorrow(place, mutable, expr.Loc()) {
			b.temp = append(b.temp, borrowRecord{place: place, mutable: mutable, loc: expr.Loc()})
		}
	}
	b.checkAddressableExpr(expr.X, place.base)
}

func (b *borrowChecker) borrowPlace(expr hir.Expr) borrowPlace {
	switch e := expr.(type) {
	case *hir.Ident:
		return borrowPlace{base: e.Symbol}
	case *hir.SelectorExpr:
		place := b.borrowPlace(e.X)
		if place.base == nil {
			return place
		}
		if e.Field == nil {
			return place
		}
		return place.withField(e.Field.Name)
	case *hir.IndexExpr:
		place := b.borrowPlace(e.X)
		if place.base == nil {
			return place
		}
		return place.withIndex()
	case *hir.ParenExpr:
		return b.borrowPlace(e.X)
	default:
		return borrowPlace{}
	}
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
	default:
		b.checkExpr(expr)
	}
}

type accessKind int

const (
	accessRead accessKind = iota
	accessWrite
)

func (b *borrowChecker) checkAccess(place borrowPlace, kind accessKind, loc *source.Location) {
	if place.base == nil {
		return
	}
	entries := b.borrows[place.base]
	if len(entries) == 0 {
		return
	}
	switch kind {
	case accessRead:
		wantMutable := true
		if entry, ok := findBorrow(entries, place.path, &wantMutable); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot access '%s' while it is mutably borrowed", place.base.Name), entry.loc, releaseLoc)
		}
	case accessWrite:
		wantMutable := true
		if entry, ok := findBorrow(entries, place.path, &wantMutable); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot modify '%s' while it is mutably borrowed", place.base.Name), entry.loc, releaseLoc)
			return
		}
		wantShared := false
		if entry, ok := findBorrow(entries, place.path, &wantShared); ok {
			releaseLoc := b.borrowReleaseLoc(place.base, entry)
			b.reportBorrowError(loc, fmt.Sprintf("cannot modify '%s' while it is immutably borrowed", place.base.Name), entry.loc, releaseLoc)
		}
	}
}

func (b *borrowChecker) addBorrow(place borrowPlace, mutable bool, loc *source.Location) bool {
	if place.base == nil {
		return true
	}
	entries := b.borrows[place.base]
	if mutable {
		if entry, ok := findBorrow(entries, place.path, nil); ok {
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
		if entry, ok := findBorrow(entries, place.path, &wantMutable); ok {
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

func (b *borrowChecker) checkReturnLifetime(expr hir.Expr) {
	if expr == nil {
		return
	}
	exprType := exprType(expr)
	if !isReferenceType(exprType) {
		return
	}

	var base *symbols.Symbol
	switch e := expr.(type) {
	case *hir.UnaryExpr:
		if isBorrowOp(e.Op.Kind) {
			base = b.borrowPlace(e.X).base
		}
	case *hir.Ident:
		if e.Symbol != nil {
			if binding, ok := b.bindings[e.Symbol]; ok {
				base = binding.place.base
			}
		}
	}
	if base == nil {
		return
	}
	if _, ok := b.locals[base]; ok {
		b.reportBorrowError(expr.Loc(), fmt.Sprintf("cannot return reference to local '%s'", base.Name), nil, nil)
	}
}

func (b *borrowChecker) reportBorrowError(loc *source.Location, msg string, borrowLoc *source.Location, releaseLoc *source.Location) {
	diag := diagnostics.NewError(msg).WithCode(diagnostics.ErrInvalidOperation)
	if loc != nil {
		diag = diag.WithPrimaryLabel(loc, "borrow conflict")
	}
	if borrowLoc != nil {
		diag = diag.WithSecondaryLabel(borrowLoc, "borrowed here")
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

func findBorrow(entries []borrowEntry, path []placeSegment, wantMutable *bool) (borrowEntry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !pathsOverlap(path, entry.path) {
			continue
		}
		if wantMutable != nil && entry.mutable != *wantMutable {
			continue
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
	return kind == tokens.BIT_AND_TOKEN || kind == tokens.MUT_REF_TOKEN
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
