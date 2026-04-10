package typechecker

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/frontend/ast"
)

// refineScope is a narrow, per-branch overlay that can override the type of a
// resolved symbol or a specific access path (used for `is` narrowing, optional
// `none` checks, and match-type arms).
//
// Base types always live in `Module.Types` and are keyed by `*symbols.Symbol`.
type refineScope struct {
	parent *refineScope
	types  map[symbols.SymbolID]typeinfo.Type
	paths  map[string]typeinfo.Type
}

type lambdaScope struct {
	owned    map[symbols.SymbolID]struct{}
	reported map[symbols.SymbolID]struct{}
}

func newRefineScope(parent *refineScope) *refineScope {
	return &refineScope{
		parent: parent,
		types:  make(map[symbols.SymbolID]typeinfo.Type),
		paths:  make(map[string]typeinfo.Type),
	}
}

func (s *refineScope) Set(sym *symbols.Symbol, typ typeinfo.Type) {
	if s == nil || sym == nil || typ == nil {
		return
	}
	s.types[sym.ID] = typ
}

func (s *refineScope) Lookup(sym *symbols.Symbol) (typeinfo.Type, bool) {
	if sym == nil {
		return nil, false
	}
	for scope := s; scope != nil; scope = scope.parent {
		if typ, ok := scope.types[sym.ID]; ok {
			return typ, true
		}
	}
	return nil, false
}

func (s *refineScope) SetPath(path string, typ typeinfo.Type) {
	if s == nil || path == "" || typ == nil {
		return
	}
	s.paths[path] = typ
}

func (s *refineScope) LookupPath(path string) (typeinfo.Type, bool) {
	if path == "" {
		return nil, false
	}
	for scope := s; scope != nil; scope = scope.parent {
		if typ, ok := scope.paths[path]; ok {
			return typ, true
		}
	}
	return nil, false
}

type checker struct {
	ctx                        *context.CompilerContext
	mod                        *context.Module
	info                       *typeinfo.ModuleInfo
	currentResult              typeinfo.Type
	unsafeDepth                int
	comptimeDepth              int
	deferDepth                 int
	typeParamScopes            []map[string]*typeinfo.TypeParam
	currentGenericFunc         *symbols.Symbol
	currentGenericRequirements []*typeinfo.GenericRequirement
	lambdaScopes               []*lambdaScope
}

func (c *checker) pushTypeParams(mod *context.Module, owner ast.Node, params []ast.TypeParam) []*typeinfo.TypeParam {
	if len(params) == 0 {
		return nil
	}
	scope := make(map[string]*typeinfo.TypeParam, len(params))
	typeParams := make([]*typeinfo.TypeParam, 0, len(params))
	for _, param := range params {
		if param.Name == nil || param.Name.Text() == "" {
			continue
		}
		tp := &typeinfo.TypeParam{Name: param.Name.Text(), Owner: owner}
		scope[tp.Name] = tp
		typeParams = append(typeParams, tp)
		if c.info != nil {
			c.info.BindNode(param.Name, tp)
		}
	}
	c.typeParamScopes = append(c.typeParamScopes, scope)
	for _, param := range params {
		if param.Name == nil || param.Name.Text() == "" {
			continue
		}
		tp := scope[param.Name.Text()]
		if param.Constraint != nil {
			tp.Constraint = c.typeFromSyntax(mod, param.Constraint)
			if c.info != nil {
				c.info.BindNode(param.Constraint, tp.Constraint)
			}
		}
	}
	return typeParams
}

func (c *checker) popTypeParams() {
	if len(c.typeParamScopes) == 0 {
		return
	}
	c.typeParamScopes = c.typeParamScopes[:len(c.typeParamScopes)-1]
}

func (c *checker) lookupTypeParam(name string) (*typeinfo.TypeParam, bool) {
	for i := len(c.typeParamScopes) - 1; i >= 0; i-- {
		if tp, ok := c.typeParamScopes[i][name]; ok {
			return tp, true
		}
	}
	return nil, false
}

func (c *checker) symbolMutable(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Kind == symbols.SymbolConst {
		return false
	}
	switch node := sym.Node.(type) {
	case *ast.LetDecl:
		return node != nil && node.IsMut
	case *ast.LetStmt:
		return node != nil && node.IsMut
	case *ast.ConstDecl, *ast.ConstStmt:
		return false
	default:
		return sym.Flags.Mutable()
	}
}

func (c *checker) bindDeclSymbol(node ast.Node, typ typeinfo.Type) {
	if c == nil || c.info == nil || node == nil || typ == nil {
		return
	}
	res := c.lookupResolution(node)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return
	}
	c.info.BindSymbol(res.Symbol, typ)
	c.ownCurrentLambdaSymbol(res.Symbol)
}

func (c *checker) declSymbol(node ast.Node) *symbols.Symbol {
	if c == nil || node == nil {
		return nil
	}
	res := c.lookupResolution(node)
	if res == nil || res.Kind != binding.ResolutionSymbol {
		return nil
	}
	return res.Symbol
}

func (c *checker) pushLambdaScope() *lambdaScope {
	if c == nil {
		return nil
	}
	scope := &lambdaScope{
		owned:    make(map[symbols.SymbolID]struct{}),
		reported: make(map[symbols.SymbolID]struct{}),
	}
	c.lambdaScopes = append(c.lambdaScopes, scope)
	return scope
}

func (c *checker) popLambdaScope() {
	if c == nil || len(c.lambdaScopes) == 0 {
		return
	}
	c.lambdaScopes = c.lambdaScopes[:len(c.lambdaScopes)-1]
}

func (c *checker) currentLambdaScope() *lambdaScope {
	if c == nil || len(c.lambdaScopes) == 0 {
		return nil
	}
	return c.lambdaScopes[len(c.lambdaScopes)-1]
}

func (c *checker) ownCurrentLambdaSymbol(sym *symbols.Symbol) {
	scope := c.currentLambdaScope()
	if scope == nil || sym == nil {
		return
	}
	scope.owned[sym.ID] = struct{}{}
}

func (c *checker) reportLambdaCapture(ident *ast.Ident, sym *symbols.Symbol) {
	scope := c.currentLambdaScope()
	if c == nil || ident == nil || sym == nil || scope == nil {
		return
	}
	if !c.isLambdaCaptureCandidate(sym) {
		return
	}
	if _, ok := scope.owned[sym.ID]; ok {
		return
	}
	if _, ok := scope.reported[sym.ID]; ok {
		return
	}
	scope.reported[sym.ID] = struct{}{}
	loc := ident.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError("capturing lambdas are not supported yet").
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "this lambda captures an outer local value"),
	)
}

func (c *checker) isLambdaCaptureCandidate(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	switch sym.Kind {
	case symbols.SymbolParam:
		return true
	case symbols.SymbolVar, symbols.SymbolConst:
		switch sym.Node.(type) {
		case *ast.LetStmt, *ast.ConstStmt, *ast.LockStmt:
			return true
		case nil:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (c *checker) bindNodeSymbolResolution(node ast.Node, sym *symbols.Symbol) {
	if c == nil || c.mod == nil || c.mod.Bindings == nil || node == nil || sym == nil {
		return
	}
	moduleKey := c.mod.Key
	importPath := c.mod.ImportPath
	if owner := c.findModuleForSymbol(sym); owner != nil {
		moduleKey = owner.Key
		importPath = owner.ImportPath
	}
	c.mod.Bindings.BindNode(node, &binding.Resolution{
		Kind:       binding.ResolutionSymbol,
		Symbol:     sym,
		ModuleKey:  moduleKey,
		ImportPath: importPath,
	})
}

func (c *checker) ownerTypeDeclForFunc(mod *context.Module, fn *ast.FuncDecl) (*context.Module, *ast.TypeDecl) {
	if c == nil || fn == nil || fn.OwnerType == nil {
		return nil, nil
	}
	ownerMod := mod
	if ownerMod == nil {
		ownerMod = c.mod
	}
	resolution := c.lookupTypeResolution(ownerMod, fn.OwnerType)
	if resolution == nil || resolution.Symbol == nil {
		return nil, nil
	}
	decl, _ := resolution.Symbol.Node.(*ast.TypeDecl)
	if decl == nil {
		return nil, nil
	}
	if symOwner := c.findModuleForSymbol(resolution.Symbol); symOwner != nil {
		ownerMod = symOwner
	}
	return ownerMod, decl
}

func (c *checker) syntaxType(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	if c == nil || expr == nil {
		return nil
	}
	if c.info != nil {
		if typ, ok := c.info.Nodes[expr]; ok && typ != nil {
			return typ
		}
	}
	typ := c.typeFromSyntax(mod, expr)
	if c.info != nil && typ != nil {
		c.info.BindNode(expr, typ)
	}
	return typ
}

func CheckModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil || mod.Bindings == nil {
		return
	}
	c := &checker{
		ctx:  ctx,
		mod:  mod,
		info: typeinfo.NewModuleInfo(),
	}

	seenSymbols := make(map[symbols.SymbolID]struct{})
	bindSymbol := func(sym *symbols.Symbol) {
		if sym == nil {
			return
		}
		if _, ok := seenSymbols[sym.ID]; ok {
			return
		}
		seenSymbols[sym.ID] = struct{}{}
		c.info.BindSymbol(sym, c.typeOfSymbol(sym))
	}
	for _, sym := range mod.ModuleScope.Symbols() {
		bindSymbol(sym)
	}
	for _, members := range mod.TypeMembers {
		for _, sym := range members {
			bindSymbol(sym)
		}
	}
	for _, methods := range mod.MethodSets {
		for _, sym := range methods {
			bindSymbol(sym)
		}
	}

	for _, decl := range mod.AST.Decls {
		c.checkDecl(decl)
	}

	mod.Types = c.info
	mod.Phase = phase.PhaseTypeChecked
}
