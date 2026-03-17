package resolver

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/table"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

type resolver struct {
	ctx       *context.CompilerContext
	mod       *context.Module
	info      *binding.ModuleInfo
	labels    []*binding.LabelBinding
	loopDepth int
	currentFn *ast.FuncDecl
}

func (r *resolver) bindDeclIdent(ident *ast.Ident, sym *symbols.Symbol) {
	if r == nil || r.info == nil || r.mod == nil || ident == nil || sym == nil {
		return
	}
	// Declaration identifiers are always local to the current module.
	r.info.BindNode(ident, &binding.Resolution{
		Kind:       binding.ResolutionSymbol,
		Symbol:     sym,
		ModuleKey:  r.mod.Key,
		ImportPath: r.mod.ImportPath,
	})
}

func (r *resolver) declareLocal(scope *table.Scope, sym *symbols.Symbol) *symbols.Symbol {
	if scope == nil || sym == nil {
		return sym
	}
	if scope.Declare(sym) {
		return sym
	}
	// Same-scope redeclaration: keep resolution stable (return the original
	// symbol) but emit a diagnostic so the user can fix it.
	if r != nil && r.ctx != nil && r.ctx.Diagnostics != nil {
		if existing, ok := scope.LookupLocal(sym.Name); ok && existing != nil {
			loc := sym.Location
			diag := diagnostics.NewError(fmt.Sprintf("redeclared symbol %q", sym.Name)).
				WithCode(diagnostics.ErrRedeclaredSymbol).
				WithPrimaryLabel(&loc, "redeclared here")
			prev := existing.Location
			diag.WithSecondaryLabel(&prev, "previous declaration is here")
			r.ctx.Diagnostics.Add(diag)
			return existing
		}
	}
	// Keep declaration bindings consistent with subsequent lookups.
	if existing, ok := scope.LookupLocal(sym.Name); ok && existing != nil {
		return existing
	}
	return sym
}

func ResolveModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil || mod.ModuleScope == nil {
		return
	}

	r := &resolver{
		ctx:  ctx,
		mod:  mod,
		info: binding.NewModuleInfo(),
	}

	r.resolveImports()

	moduleScope := table.New(mod.ModuleScope)
	for _, decl := range mod.AST.Decls {
		r.resolveDecl(moduleScope, decl)
	}

	mod.Bindings = r.info
	mod.Phase = phase.PhaseResolved
}

func (r *resolver) resolveImports() {
	seen := make(map[string]*binding.ImportBinding)
	for _, imp := range r.mod.AST.Imports {
		if imp == nil {
			continue
		}
		resolved, err := r.ctx.ResolveImport(r.mod, ast.ExprText(imp.Path))
		if err != nil {
			continue
		}
		name := ast.ExprText(imp.Alias)
		segments := []string{lastImportSegment(resolved.ImportPath)}
		if name != "" {
			segments = []string{name}
		}
		b := &binding.ImportBinding{
			Name:       name,
			ImportPath: resolved.ImportPath,
			ModuleKey:  resolved.Key,
			Segments:   segments,
			Location:   importBindingLocation(imp),
		}
		key := b.Key()
		if prev, ok := seen[key]; ok {
			diag := diagnostics.NewError(fmt.Sprintf("duplicate import binding %q", key)).
				WithCode(diagnostics.ErrAmbiguousImport).
				WithPrimaryLabel(&b.Location, "duplicate import binding")
			diag.WithSecondaryLabel(&prev.Location, "previous import binding is here")
			r.ctx.Diagnostics.Add(diag)
			continue
		}
		seen[key] = b
		r.info.Imports = append(r.info.Imports, b)
	}
}

func lastImportSegment(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func (r *resolver) resolveDecl(scope *table.Scope, decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.LetDecl:
		r.resolveType(scope, d.Type)
		r.resolveExpr(scope, d.Value)
		if d.Name != nil && r.mod.ModuleScope != nil {
			if sym, ok := r.mod.ModuleScope.LookupLocal(d.Name.Text()); ok {
				r.bindDeclIdent(d.Name, sym)
			}
		}
	case *ast.ConstDecl:
		r.resolveType(scope, d.Type)
		r.resolveExpr(scope, d.Value)
		if d.Name != nil && r.mod.ModuleScope != nil {
			if sym, ok := r.mod.ModuleScope.LookupLocal(d.Name.Text()); ok {
				r.bindDeclIdent(d.Name, sym)
			}
		}
	case *ast.TypeDecl:
		r.resolveType(scope, d.Type)
		if d.Name != nil && r.mod.ModuleScope != nil {
			if sym, ok := r.mod.ModuleScope.LookupLocal(d.Name.Text()); ok {
				r.bindDeclIdent(d.Name, sym)
			}
		}
	case *ast.FuncDecl:
		if d.Receiver != nil {
			r.resolveType(scope, d.Receiver.Type)
		}
		if sym, ok := r.lookupFunctionSymbol(d); ok {
			r.info.BindFunctionSymbol(d, sym)
			if d.Name != nil {
				r.bindDeclIdent(d.Name, sym)
			}
		}
		prevFn := r.currentFn
		r.currentFn = d
		defer func() { r.currentFn = prevFn }()
		funcScope := table.New(scope)
		if d.Receiver != nil {
			sym := symbols.New(d.Receiver.Name.Text(), symbols.SymbolParam, nil)
			sym.Location = d.Receiver.Name.Loc()
			sym.Mutable = true
			declared := r.declareLocal(funcScope, sym)
			r.info.AddFunctionLocal(d, declared)
			r.bindDeclIdent(d.Receiver.Name, declared)
		}
		for _, param := range d.Params {
			r.resolveType(scope, param.Type)
			sym := symbols.New(param.Name.Text(), symbols.SymbolParam, nil)
			sym.Location = param.Name.Loc()
			sym.Mutable = false
			declared := r.declareLocal(funcScope, sym)
			r.info.AddFunctionLocal(d, declared)
			r.bindDeclIdent(param.Name, declared)
		}
		r.resolveType(scope, d.Result)
		r.resolveStmt(funcScope, d.Body)
	}
}

func (r *resolver) resolveStmt(scope *table.Scope, stmt ast.Stmt) {
	if isNilStmt(stmt) {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		blockScope := table.New(scope)
		for _, child := range s.Stmts {
			r.resolveStmt(blockScope, child)
		}
	case *ast.LetStmt:
		r.resolveType(scope, s.Type)
		r.resolveExpr(scope, s.Value)
		sym := symbols.New(s.Name.Text(), symbols.SymbolVar, s)
		sym.Location = s.Name.Loc()
		sym.Mutable = s.IsMut
		declared := r.declareLocal(scope, sym)
		r.addFunctionLocal(declared)
		r.bindDeclIdent(s.Name, declared)
	case *ast.ConstStmt:
		r.resolveType(scope, s.Type)
		r.resolveExpr(scope, s.Value)
		sym := symbols.New(s.Name.Text(), symbols.SymbolConst, s)
		sym.Location = s.Name.Loc()
		sym.Mutable = false
		declared := r.declareLocal(scope, sym)
		r.addFunctionLocal(declared)
		r.bindDeclIdent(s.Name, declared)
	case *ast.ReturnStmt:
		r.resolveExpr(scope, s.Value)
	case *ast.ExprStmt:
		r.resolveExpr(scope, s.Value)
	case *ast.AssignStmt:
		if ident, ok := s.Left.(*ast.Ident); !ok || ident.Text() != "_" {
			r.resolveExpr(scope, s.Left)
		}
		r.resolveExpr(scope, s.Right)
	case *ast.IfStmt:
		r.resolveExpr(scope, s.Cond)
		r.resolveStmt(scope, s.Then)
		r.resolveStmt(scope, s.Else)
	case *ast.MatchStmt:
		r.resolveExpr(scope, s.Value)
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			armScope := table.New(scope)
			if arm.TypePattern != nil {
				r.resolveType(scope, arm.TypePattern)
			} else if !arm.Wildcard {
				r.resolveExpr(scope, arm.Pattern)
			}
			r.resolveStmt(armScope, arm.Body)
		}
	case *ast.WhileStmt:
		r.resolveExpr(scope, s.Cond)
		r.loopDepth++
		r.resolveStmt(scope, s.Body)
		r.loopDepth--
	case *ast.ForStmt:
		loopScope := table.New(scope)
		r.resolveExpr(loopScope, s.Iterable)
		if s.Index != nil {
			sym := symbols.New(s.Index.Text(), symbols.SymbolVar, nil)
			sym.Location = s.Index.Loc()
			sym.Mutable = false
			declared := r.declareLocal(loopScope, sym)
			r.addFunctionLocal(declared)
			r.bindDeclIdent(s.Index, declared)
		}
		if s.Value != nil {
			sym := symbols.New(s.Value.Text(), symbols.SymbolVar, nil)
			sym.Location = s.Value.Loc()
			sym.Mutable = false
			declared := r.declareLocal(loopScope, sym)
			r.addFunctionLocal(declared)
			r.bindDeclIdent(s.Value, declared)
		}
		r.loopDepth++
		r.resolveStmt(loopScope, s.Body)
		r.loopDepth--
	case *ast.LabelStmt:
		label := &binding.LabelBinding{Name: s.Name.Text(), Stmt: s.Stmt, Location: s.Name.Loc()}
		r.labels = append(r.labels, label)
		r.resolveStmt(scope, s.Stmt)
		r.labels = r.labels[:len(r.labels)-1]
	case *ast.BreakStmt:
		r.resolveBreakLike(ast.ExprText(s.Label), stmt, diagnostics.ErrInvalidBreak, "break")
	case *ast.ContinueStmt:
		r.resolveBreakLike(ast.ExprText(s.Label), stmt, diagnostics.ErrInvalidContinue, "continue")
	case *ast.DeferStmt:
		r.resolveStmt(scope, s.Body)
	case *ast.ReleaseStmt:
		r.resolveExpr(scope, s.Value)
	case *ast.PanicStmt:
		r.resolveExpr(scope, s.Value)
	case *ast.LockStmt:
		r.resolveExpr(scope, s.Value)
		lockScope := table.New(scope)
		sym := symbols.New(s.Name.Text(), symbols.SymbolVar, s)
		sym.Location = s.Name.Loc()
		sym.Mutable = true
		declared := r.declareLocal(lockScope, sym)
		r.addFunctionLocal(declared)
		r.bindDeclIdent(s.Name, declared)
		r.resolveStmt(lockScope, s.Body)
	case *ast.UnsafeStmt:
		r.resolveStmt(scope, s.Body)
	}
}

func (r *resolver) addFunctionLocal(sym *symbols.Symbol) {
	if r == nil || r.currentFn == nil || sym == nil {
		return
	}
	r.info.AddFunctionLocal(r.currentFn, sym)
}

func (r *resolver) lookupFunctionSymbol(fn *ast.FuncDecl) (*symbols.Symbol, bool) {
	if r == nil || r.mod == nil || fn == nil {
		return nil, false
	}
	if fn.Receiver == nil && r.mod.ModuleScope != nil {
		if sym, ok := r.mod.ModuleScope.LookupLocal(fn.Name.Text()); ok {
			return sym, true
		}
	}
	for _, methods := range r.mod.MethodSets {
		for _, sym := range methods {
			if sym != nil && sym.Node == fn {
				return sym, true
			}
		}
	}
	return nil, false
}

func isNilStmt(stmt ast.Stmt) bool {
	if stmt == nil {
		return true
	}
	v := reflect.ValueOf(stmt)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func (r *resolver) resolveBreakLike(labelName string, node ast.Node, code string, keyword string) {
	if labelName == "" {
		if r.loopDepth == 0 {
			loc := breakLikeLocation(node)
			r.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("%s is not inside a loop", keyword)).
					WithCode(code).
					WithPrimaryLabel(&loc, "invalid control-flow target"),
			)
		}
		return
	}
	for i := len(r.labels) - 1; i >= 0; i-- {
		label := r.labels[i]
		if label.Name != labelName {
			continue
		}
		if !isLoopStmt(label.Stmt) {
			loc := breakLikeLocation(node)
			r.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("%s label %q does not name a loop", keyword, labelName)).
					WithCode(code).
					WithPrimaryLabel(&loc, "invalid labeled control-flow target"),
			)
			return
		}
		r.info.BindLabel(node, label)
		return
	}
	loc := breakLikeLocation(node)
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("unknown label %q", labelName)).
			WithCode(code).
			WithPrimaryLabel(&loc, "unknown labeled control-flow target"),
	)
}

func importBindingLocation(imp *ast.ImportDecl) source.Location {
	if imp == nil {
		return source.Location{}
	}
	if imp.Alias != nil {
		return imp.Alias.Loc()
	}
	if imp.Path != nil {
		return imp.Path.Loc()
	}
	return imp.Location
}

func breakLikeLocation(node ast.Node) source.Location {
	switch n := node.(type) {
	case *ast.BreakStmt:
		if n.Label != nil {
			return n.Label.Loc()
		}
		return n.Location
	case *ast.ContinueStmt:
		if n.Label != nil {
			return n.Label.Loc()
		}
		return n.Location
	default:
		return node.Loc()
	}
}

func isLoopStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.WhileStmt, *ast.ForStmt:
		return true
	default:
		return false
	}
}

func (r *resolver) resolveExpr(scope *table.Scope, expr ast.Expr) {
	switch e := expr.(type) {
	case nil:
		return
	case *ast.Ident:
		r.resolveExprPath(scope, e)
	case *ast.PrefixExpr:
		r.resolveExpr(scope, e.Right)
	case *ast.BinaryExpr:
		r.resolveExpr(scope, e.Left)
		r.resolveExpr(scope, e.Right)
	case *ast.PostfixExpr:
		r.resolveExpr(scope, e.Left)
	case *ast.CallExpr:
		r.resolveExpr(scope, e.Callee)
		for _, arg := range e.Args {
			r.resolveExpr(scope, arg)
		}
		for _, typ := range e.TypeArgs {
			r.resolveType(scope, typ)
		}
	case *ast.SelectorExpr:
		r.resolveExpr(scope, e.Left)
	case *ast.CastExpr:
		r.resolveExpr(scope, e.Left)
		r.resolveType(scope, e.Type)
	case *ast.IsExpr:
		r.resolveExpr(scope, e.Left)
		r.resolveType(scope, e.Type)
	case *ast.MatchExpr:
		r.resolveExpr(scope, e.Value)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			armScope := table.New(scope)
			if arm.TypePattern != nil {
				r.resolveType(scope, arm.TypePattern)
			} else if !arm.Wildcard {
				r.resolveExpr(scope, arm.Pattern)
			}
			r.resolveStmt(armScope, arm.Body)
		}
	case *ast.CatchExpr:
		r.resolveExpr(scope, e.Left)
		if e.Handler != nil {
			handlerScope := table.New(scope)
			if e.Payload != nil {
				sym := symbols.New(e.Payload.Text(), symbols.SymbolVar, nil)
				sym.Location = e.Payload.Loc()
				sym.Mutable = false
				declared := r.declareLocal(handlerScope, sym)
				r.addFunctionLocal(declared)
				r.bindDeclIdent(e.Payload, declared)
			}
			r.resolveStmt(handlerScope, e.Handler)
			return
		}
		r.resolveExpr(scope, e.Fallback)
	case *ast.CompositeLit:
		for _, item := range e.Items {
			r.resolveExpr(scope, item.Value)
		}
	case *ast.IndexExpr:
		r.resolveExpr(scope, e.Left)
		r.resolveExpr(scope, e.Index)
	}
}

func (r *resolver) resolveType(scope *table.Scope, typ ast.TypeExpr) {
	switch t := typ.(type) {
	case nil:
		return
	case *ast.NamedType:
		r.resolveTypePath(scope, t)
	case *ast.PointerType:
		r.resolveType(scope, t.Inner)
	case *ast.RefType:
		r.resolveType(scope, t.Inner)
	case *ast.RawPtrType:
		r.resolveType(scope, t.Inner)
	case *ast.OptionalType:
		r.resolveType(scope, t.Inner)
	case *ast.ErrorUnionType:
		r.resolveType(scope, t.Error)
		r.resolveType(scope, t.Value)
	case *ast.ArrayType:
		if ident, ok := t.Size.(*ast.Ident); !ok || ident.Text() != "_" {
			r.resolveExpr(scope, t.Size)
		}
		r.resolveType(scope, t.Inner)
	case *ast.TupleType:
		for _, elem := range t.Elems {
			r.resolveType(scope, elem)
		}
	case *ast.StructType:
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			r.resolveType(scope, field.Type)
			r.resolveExpr(scope, field.Default)
		}
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			r.resolveType(scope, field.Type)
			r.resolveExpr(scope, field.Default)
		}
	case *ast.InterfaceType:
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			for _, param := range method.Params {
				r.resolveType(scope, param.Type)
			}
			r.resolveType(scope, method.Result)
		}
	case *ast.UnionType:
		for _, member := range t.Members {
			r.resolveType(scope, member)
		}
	}
}

func (r *resolver) resolveExprPath(scope *table.Scope, ident *ast.Ident) {
	if ident == nil || len(ident.Path) == 0 {
		return
	}
	if len(ident.Path) == 1 {
		if sym, ok := scope.Lookup(ident.Path[0]); ok {
			moduleKey := r.mod.Key
			importPath := r.mod.ImportPath
			if owner := r.findModuleForSymbol(sym); owner != nil {
				moduleKey = owner.Key
				importPath = owner.ImportPath
			}
			r.info.BindNode(ident, &binding.Resolution{Kind: binding.ResolutionSymbol, Symbol: sym, ModuleKey: moduleKey, ImportPath: importPath})
			return
		}
		r.reportUndefined(ident.Location, ident.Path[0])
		return
	}

	r.resolveQualifiedPath(scope, ident.Path, ident, false)
}

func (r *resolver) resolveTypePath(scope *table.Scope, typ *ast.NamedType) {
	if typ == nil || len(typ.Path) == 0 {
		return
	}
	if len(typ.Path) == 1 {
		if isPredeclaredType(typ.Path[0]) {
			return
		}
		if sym, ok := scope.Lookup(typ.Path[0]); ok && sym.Kind == symbols.SymbolType {
			moduleKey := r.mod.Key
			importPath := r.mod.ImportPath
			if owner := r.findModuleForSymbol(sym); owner != nil {
				moduleKey = owner.Key
				importPath = owner.ImportPath
			}
			r.info.BindNode(typ, &binding.Resolution{Kind: binding.ResolutionSymbol, Symbol: sym, ModuleKey: moduleKey, ImportPath: importPath})
			return
		}
		r.reportUndefined(typ.Location, typ.Path[0])
		return
	}

	if resolution, ok := r.resolveUnionMemberTypePath(scope, typ); ok {
		r.info.BindNode(typ, resolution)
		return
	}

	r.resolveQualifiedPath(scope, typ.Path, typ, true)
}

func (r *resolver) resolveUnionMemberTypePath(scope *table.Scope, typ *ast.NamedType) (*binding.Resolution, bool) {
	if typ == nil || len(typ.Path) < 2 {
		return nil, false
	}
	prefix := typ.Path[:len(typ.Path)-1]
	memberName := typ.Path[len(typ.Path)-1]
	sym, owner, ok := r.lookupTypeSymbolPath(scope, prefix)
	if !ok || sym == nil {
		return nil, false
	}
	decl, _ := sym.Node.(*ast.TypeDecl)
	unionDecl, ok := decl.Type.(*ast.UnionType)
	if !ok || unionDecl == nil || !unionHasNamedMember(unionDecl, memberName) {
		return nil, false
	}
	moduleKey := ""
	importPath := ""
	if owner != nil {
		moduleKey = owner.Key
		importPath = owner.ImportPath
	}
	return &binding.Resolution{
		Kind:       binding.ResolutionSymbol,
		Symbol:     sym,
		ModuleKey:  moduleKey,
		ImportPath: importPath,
		Remaining:  []string{memberName},
	}, true
}

func unionHasNamedMember(unionDecl *ast.UnionType, name string) bool {
	if unionDecl == nil {
		return false
	}
	for _, member := range unionDecl.Members {
		named, ok := member.(*ast.NamedType)
		if !ok || named == nil || len(named.Path) != 1 {
			continue
		}
		if named.Path[0] == name {
			return true
		}
	}
	return false
}

func (r *resolver) lookupTypeSymbolPath(scope *table.Scope, path []string) (*symbols.Symbol, *context.Module, bool) {
	if len(path) == 0 {
		return nil, nil, false
	}
	if len(path) == 1 {
		sym, ok := scope.Lookup(path[0])
		if !ok || sym.Kind != symbols.SymbolType {
			return nil, nil, false
		}
		owner := r.findModuleForSymbol(sym)
		if owner == nil {
			owner = r.mod
		}
		return sym, owner, true
	}
	imp, matched := r.matchImport(path)
	if imp == nil {
		return nil, nil, false
	}
	mod, ok := r.ctx.GetModule(imp.ModuleKey)
	if !ok || mod == nil || mod.ModuleScope == nil {
		return nil, nil, false
	}
	remaining := path[matched:]
	if len(remaining) != 1 {
		return nil, nil, false
	}
	sym, ok := mod.ModuleScope.LookupLocal(remaining[0])
	if !ok || sym.Kind != symbols.SymbolType {
		return nil, nil, false
	}
	return sym, mod, true
}

func (r *resolver) resolveQualifiedPath(scope *table.Scope, path []string, node ast.Node, typeOnly bool) {
	imp, matched := r.matchImport(path)
	var sym *symbols.Symbol
	if candidate, ok := scope.Lookup(path[0]); ok {
		if !typeOnly || candidate.Kind == symbols.SymbolType {
			sym = candidate
		}
	}

	if imp != nil && sym != nil {
		loc := node.Loc()
		r.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("ambiguous qualified path %q", strings.Join(path, "::"))).
				WithCode(diagnostics.ErrAmbiguousImport).
				WithPrimaryLabel(&loc, "matches both an import and a local symbol"),
		)
		return
	}

	if imp != nil {
		if resolution, ok := r.resolveModulePath(imp, path[matched:], node, typeOnly); ok {
			r.info.BindNode(node, resolution)
			return
		}
		return
	}

	if sym != nil {
		if resolution, ok := r.resolveSymbolPath(r.mod, sym, path[1:], node, typeOnly, false); ok {
			r.info.BindNode(node, resolution)
			return
		}
		return
	}

	r.reportUndefined(node.Loc(), path[0])
}

func (r *resolver) resolveModulePath(imp *binding.ImportBinding, remaining []string, node ast.Node, typeOnly bool) (*binding.Resolution, bool) {
	if imp == nil {
		return nil, false
	}
	if len(remaining) == 0 {
		return &binding.Resolution{
			Kind:       binding.ResolutionModule,
			ModuleKey:  imp.ModuleKey,
			ImportPath: imp.ImportPath,
		}, true
	}

	target, ok := r.ctx.GetModule(imp.ModuleKey)
	if !ok || target == nil || target.ModuleScope == nil {
		return &binding.Resolution{
			Kind:       binding.ResolutionModule,
			ModuleKey:  imp.ModuleKey,
			ImportPath: imp.ImportPath,
			Remaining:  append([]string(nil), remaining...),
		}, true
	}

	sym, ok := target.ModuleScope.LookupLocal(remaining[0])
	if !ok {
		r.reportMissingInModule(node.Loc(), remaining[0], target.ImportPath)
		return nil, false
	}
	if !sym.Exported {
		r.reportNotExportedFromModule(node.Loc(), sym.Name, target.ImportPath)
		return nil, false
	}
	return r.resolveSymbolPath(target, sym, remaining[1:], node, typeOnly, true)
}

func (r *resolver) resolveSymbolPath(mod *context.Module, sym *symbols.Symbol, remaining []string, node ast.Node, typeOnly bool, requireExported bool) (*binding.Resolution, bool) {
	if sym == nil {
		return nil, false
	}
	if requireExported && !sym.Exported {
		modulePath := ""
		if mod != nil {
			modulePath = mod.ImportPath
		}
		r.reportNotExportedFromModule(node.Loc(), sym.Name, modulePath)
		return nil, false
	}
	if len(remaining) == 0 {
		if typeOnly && sym.Kind != symbols.SymbolType {
			r.reportInvalidType(node.Loc(), sym.Name)
			return nil, false
		}
		moduleKey := ""
		importPath := ""
		if mod != nil {
			moduleKey = mod.Key
			importPath = mod.ImportPath
		}
		return &binding.Resolution{
			Kind:       binding.ResolutionSymbol,
			Symbol:     sym,
			ModuleKey:  moduleKey,
			ImportPath: importPath,
		}, true
	}
	if sym.Kind != symbols.SymbolType {
		r.reportUndefined(node.Loc(), remaining[0])
		return nil, false
	}

	member, ok := r.lookupTypeMember(mod, sym.Name, remaining[0])
	if !ok {
		r.reportMissingInType(node.Loc(), remaining[0], sym.Name)
		return nil, false
	}
	if requireExported && !member.Exported {
		owner := sym.Name
		if mod != nil && mod.ImportPath != "" {
			owner = mod.ImportPath + "::" + owner
		}
		r.reportNotExportedFromType(node.Loc(), member.Name, owner)
		return nil, false
	}
	if len(remaining) > 1 {
		r.reportUndefined(node.Loc(), remaining[1])
		return nil, false
	}
	if typeOnly {
		r.reportInvalidType(node.Loc(), remaining[0])
		return nil, false
	}
	moduleKey := ""
	importPath := ""
	if mod != nil {
		moduleKey = mod.Key
		importPath = mod.ImportPath
	}
	return &binding.Resolution{
		Kind:       binding.ResolutionSymbol,
		Symbol:     member,
		ModuleKey:  moduleKey,
		ImportPath: importPath,
	}, true
}

func (r *resolver) lookupTypeMember(mod *context.Module, typeName, memberName string) (*symbols.Symbol, bool) {
	if mod == nil || mod.TypeMembers == nil {
		return nil, false
	}
	members := mod.TypeMembers[typeName]
	if members == nil {
		return nil, false
	}
	sym, ok := members[memberName]
	return sym, ok
}

func (r *resolver) matchImport(path []string) (*binding.ImportBinding, int) {
	var best *binding.ImportBinding
	bestLen := 0
	for _, imp := range r.info.Imports {
		if imp == nil || len(imp.Segments) == 0 || len(imp.Segments) > len(path) {
			continue
		}
		match := true
		for i, seg := range imp.Segments {
			if path[i] != seg {
				match = false
				break
			}
		}
		if match && len(imp.Segments) > bestLen {
			best = imp
			bestLen = len(imp.Segments)
		}
	}
	return best, bestLen
}

func (r *resolver) reportUndefined(loc source.Location, name string) {
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("undefined symbol %q", name)).
			WithCode(diagnostics.ErrUndefinedSymbol).
			WithPrimaryLabel(&loc, "cannot resolve this name"),
	)
}

func (r *resolver) reportMissingInModule(loc source.Location, name, modulePath string) {
	msg := fmt.Sprintf("symbol %q not found in module %q", name, modulePath)
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrUndefinedSymbol).
			WithPrimaryLabel(&loc, "symbol is not defined in this module"),
	)
}

func (r *resolver) reportMissingInType(loc source.Location, name, typeName string) {
	msg := fmt.Sprintf("symbol %q not found in type %q", name, typeName)
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrUndefinedSymbol).
			WithPrimaryLabel(&loc, "type member does not exist"),
	)
}

func (r *resolver) reportNotExportedFromModule(loc source.Location, name, modulePath string) {
	msg := fmt.Sprintf("symbol %q is not exported", name)
	if modulePath != "" {
		msg = fmt.Sprintf("symbol %q is not exported from %q", name, modulePath)
	}
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrSymbolNotExported).
			WithPrimaryLabel(&loc, "symbol is not exported by this module"),
	)
}

func (r *resolver) reportNotExportedFromType(loc source.Location, name, owner string) {
	msg := fmt.Sprintf("symbol %q is not exported", name)
	if owner != "" {
		msg = fmt.Sprintf("symbol %q is not exported from %q", name, owner)
	}
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrSymbolNotExported).
			WithPrimaryLabel(&loc, "symbol is not exported by this type"),
	)
}

func (r *resolver) reportInvalidType(loc source.Location, name string) {
	r.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("%q does not name a type", name)).
			WithCode(diagnostics.ErrInvalidType).
			WithPrimaryLabel(&loc, "expected a type here"),
	)
}

func isPredeclaredType(name string) bool {
	return tokens.IsBuiltinType(name) || name == "Type"
}

func (r *resolver) findModuleForSymbol(sym *symbols.Symbol) *context.Module {
	if r == nil || r.ctx == nil || sym == nil {
		return nil
	}
	if mod := r.ctx.Prelude; mod != nil {
		if mod.ModuleScope != nil && slices.Contains(mod.ModuleScope.Symbols(), sym) {
			return mod
		}
		for _, methods := range mod.MethodSets {
			for _, candidate := range methods {
				if candidate == sym {
					return mod
				}
			}
		}
		for _, members := range mod.TypeMembers {
			for _, candidate := range members {
				if candidate == sym {
					return mod
				}
			}
		}
	}
	for _, mod := range r.ctx.Modules() {
		if mod == nil {
			continue
		}
		if mod.ModuleScope != nil && slices.Contains(mod.ModuleScope.Symbols(), sym) {
			return mod
		}
		for _, methods := range mod.MethodSets {
			for _, candidate := range methods {
				if candidate == sym {
					return mod
				}
			}
		}
		for _, members := range mod.TypeMembers {
			for _, candidate := range members {
				if candidate == sym {
					return mod
				}
			}
		}
	}
	return nil
}
