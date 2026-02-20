package gen

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/hir"
	"compiler/internal/semantics/narrowing"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/semantics/typechecker"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

// Generator lowers typed AST into HIR.
type Generator struct {
	ctx *context_v2.CompilerContext
	mod *context_v2.Module
}

// New creates a new HIR generator for a module.
func New(ctx *context_v2.CompilerContext, mod *context_v2.Module) *Generator {
	return &Generator{ctx: ctx, mod: mod}
}

// GenerateModule generates HIR for the module AST.
func (g *Generator) GenerateModule() *hir.Module {
	if g == nil || g.mod == nil || g.mod.AST == nil {
		return nil
	}

	prevScope := g.mod.CurrentScope
	if g.mod.ModuleScope != nil {
		g.mod.CurrentScope = g.mod.ModuleScope
	}
	defer func() {
		g.mod.CurrentScope = prevScope
	}()

	items := make([]hir.Node, 0, len(g.mod.AST.Nodes))
	for _, node := range g.mod.AST.Nodes {
		if lowered := g.lowerNode(node); lowered != nil {
			items = append(items, lowered)
		}
	}

	return &hir.Module{
		ImportPath: g.mod.ImportPath,
		Items:      items,
		Location:   locFromNode(g.mod.AST),
	}
}

func (g *Generator) lowerNode(node ast.Node) hir.Node {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *ast.VarDecl:
		return g.lowerVarDecl(n)
	case *ast.ConstDecl:
		return g.lowerConstDecl(n)
	case *ast.TypeDecl:
		return g.lowerTypeDecl(n)
	case *ast.ConstraintDecl:
		// Constraints are compile-time only and do not lower to HIR values yet.
		return nil
	case *ast.FuncDecl:
		return g.lowerFuncDecl(n)
	case *ast.MethodDecl:
		return g.lowerMethodDecl(n)
	case *ast.DeclStmt:
		return g.lowerDeclStmt(n)
	case *ast.AssignStmt:
		return g.lowerAssignStmt(n)
	case *ast.ReturnStmt:
		return g.lowerReturnStmt(n)
	case *ast.ImportStmt:
		return g.lowerImportStmt(n)
	case *ast.BreakStmt:
		return &hir.BreakStmt{Location: locFromNode(n)}
	case *ast.ContinueStmt:
		return &hir.ContinueStmt{Location: locFromNode(n)}
	case *ast.ExprStmt:
		return g.lowerExprStmt(n)
	case *ast.Block:
		return g.lowerBlock(n)
	case *ast.IfStmt:
		return g.lowerIfStmt(n)
	case *ast.ForStmt:
		return g.lowerForStmt(n)
	case *ast.WhileStmt:
		return g.lowerWhileStmt(n)
	case *ast.MatchStmt:
		return g.lowerMatchStmt(n)
	case *ast.DeferStmt:
		return g.lowerDeferStmt(n)
	case *ast.Invalid:
		return &hir.Invalid{Location: locFromNode(n)}
	default:
		g.reportUnsupported("node", node)
		return &hir.Invalid{Location: locFromNode(node)}
	}
}

func (g *Generator) lowerExpr(expr ast.Expression) hir.Expr {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *ast.BasicLit:
		return &hir.Literal{
			Kind:     lowerLiteralKind(e.Kind),
			Value:    e.Value,
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.IdentifierExpr:
		return g.lowerIdentExpr(e)
	case *ast.BinaryExpr:
		binExpr := &hir.BinaryExpr{
			X:        g.lowerExpr(e.X),
			Op:       e.Op,
			Y:        g.lowerExpr(e.Y),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
		// For 'is' operator, extract the target type from the TypeExpr on the RHS
		if e.Op.Kind == tokens.IS_TOKEN {
			if typeExpr, ok := e.Y.(*ast.TypeExpr); ok && typeExpr.Type != nil {
				// Try to resolve the type from the TypeNode
				binExpr.TargetType = typechecker.TypeFromTypeNodeWithContext(g.ctx, g.mod, typeExpr.Type)
				// Fallback to expr type inference if needed
				if binExpr.TargetType == nil || binExpr.TargetType.Equals(types.TypeUnknown) {
					binExpr.TargetType = g.exprType(e.Y)
				}
			}
		}
		return binExpr
	case *ast.UnaryExpr:
		return &hir.UnaryExpr{
			Op:       e.Op,
			X:        g.lowerExpr(e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.SpreadExpr:
		return &hir.SpreadExpr{
			X:        g.lowerExpr(e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.DerefExpr:
		return &hir.DerefExpr{
			X:        g.lowerExpr(e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.PrefixExpr:
		return &hir.PrefixExpr{
			Op:       e.Op,
			X:        g.autoDerefExpr(expr, e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.PostfixExpr:
		return &hir.PostfixExpr{
			X:        g.autoDerefExpr(expr, e.X),
			Op:       e.Op,
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.ErrorPropagateExpr:
		return &hir.ResultPropagate{
			Value:    g.lowerExpr(e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.CallExpr:
		return g.lowerCallExpr(e)
	case *ast.SelectorExpr:
		return g.lowerSelectorExpr(e)
	case *ast.ScopeResolutionExpr:
		return g.lowerScopeResolutionExpr(e)
	case *ast.RangeExpr:
		return &hir.RangeExpr{
			Start:     g.lowerExpr(e.Start),
			End:       g.lowerExpr(e.End),
			Incr:      g.lowerExpr(e.Incr),
			Inclusive: e.Inclusive,
			Type:      g.exprType(expr),
			Location:  locFromNode(e),
		}
	case *ast.IndexExpr:
		return &hir.IndexExpr{
			X:        g.autoDerefExpr(expr, e.X),
			Index:    g.lowerExpr(e.Index),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.CastExpr:
		return g.lowerCastExpr(e)
	case *ast.CoalescingExpr:
		return &hir.CoalescingExpr{
			Cond:     g.lowerExpr(e.Cond),
			Default:  g.lowerExpr(e.Default),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.PipeExpr:
		// Transform pipe expression into a call expression
		return g.lowerPipeExpr(e)
	case *ast.ForkExpr:
		return &hir.ForkExpr{
			Call:     g.lowerExpr(e.Call),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.ParenExpr:
		return &hir.ParenExpr{
			X:        g.lowerExpr(e.X),
			Type:     g.exprType(expr),
			Location: locFromNode(e),
		}
	case *ast.CompositeLit:
		return g.lowerCompositeLit(e)
	case *ast.KeyValueExpr:
		return &hir.KeyValueExpr{
			// Keys are labels (struct fields) or map-key expressions; do not auto-move here.
			Key:      g.lowerExpr(e.Key),
			Value:    g.lowerOwnedTransferExpr(e.Value, nil, false),
			Location: locFromNode(e),
		}
	case *ast.FuncLit:
		return g.lowerFuncLit(e)
	case *ast.TypeExpr:
		// TypeExpr is used in 'is' operator with union types
		// For HIR, we convert the type to an identifier expression
		// The type information is preserved through the module's type system
		return g.lowerTypeExprToIdent(e)
	case *ast.TypeCheckPattern:
		// Type check pattern: is Type
		// Lower to a BinaryExpr with 'is' operator
		// Get the match expression from context (we'll need to pass this differently)
		// For now, create a special HIR node for this
		return &hir.TypeCheckPattern{
			Type:     typechecker.TypeFromTypeNodeWithContext(g.ctx, g.mod, e.Type),
			Location: locFromNode(e),
		}
	case *ast.RangeCheckPattern:
		// Range check pattern: in Range
		// Lower to a range check expression
		return &hir.RangeCheckPattern{
			Range:    g.lowerExpr(e.Range),
			Location: locFromNode(e),
		}
	case *ast.Invalid:
		return &hir.Invalid{Location: locFromNode(e)}
	default:
		g.reportUnsupported("expression", expr)
		return &hir.Invalid{Location: locFromNode(expr)}
	}
}

func (g *Generator) autoDerefExpr(access ast.Expression, base ast.Expression) hir.Expr {
	lowered := g.lowerExpr(base)
	if access == nil || base == nil {
		return lowered
	}
	if !typechecker.AutoDerefAllowed(access) {
		return lowered
	}
	baseType := g.exprType(base)
	if baseType == nil || baseType.Equals(types.TypeUnknown) {
		return lowered
	}
	baseType = types.UnwrapType(baseType)
	ref, ok := baseType.(*types.ReferenceType)
	if !ok {
		return lowered
	}
	return &hir.DerefExpr{
		X:        lowered,
		Type:     ref.Inner,
		Location: locFromNode(base),
	}
}

func (g *Generator) lowerDeclStmt(stmt *ast.DeclStmt) *hir.DeclStmt {
	if stmt == nil {
		return nil
	}
	return &hir.DeclStmt{
		Decl:     g.lowerDecl(stmt.Decl),
		Location: locFromNode(stmt),
	}
}

func (g *Generator) lowerDecl(decl ast.Decl) hir.Decl {
	if decl == nil {
		return nil
	}
	switch d := decl.(type) {
	case *ast.VarDecl:
		return g.lowerVarDecl(d)
	case *ast.ConstDecl:
		return g.lowerConstDecl(d)
	case *ast.TypeDecl:
		return g.lowerTypeDecl(d)
	case *ast.ConstraintDecl:
		return nil
	case *ast.FuncDecl:
		return g.lowerFuncDecl(d)
	case *ast.MethodDecl:
		return g.lowerMethodDecl(d)
	default:
		g.reportUnsupported("declaration", decl)
		return &hir.Invalid{Location: locFromNode(decl)}
	}
}

func (g *Generator) lowerVarDecl(decl *ast.VarDecl) *hir.VarDecl {
	if decl == nil {
		return nil
	}
	items := make([]hir.DeclItem, 0, len(decl.Decls))
	for _, item := range decl.Decls {
		items = append(items, g.lowerDeclItem(item))
	}
	return &hir.VarDecl{Decls: items, Location: locFromNode(decl)}
}

func (g *Generator) lowerConstDecl(decl *ast.ConstDecl) *hir.ConstDecl {
	if decl == nil {
		return nil
	}
	items := make([]hir.DeclItem, 0, len(decl.Decls))
	for _, item := range decl.Decls {
		items = append(items, g.lowerDeclItem(item))
	}
	return &hir.ConstDecl{Decls: items, Location: locFromNode(decl)}
}

func (g *Generator) lowerDeclItem(item ast.DeclItem) hir.DeclItem {
	var ident *hir.Ident
	var name string
	if item.Name != nil {
		name = item.Name.Name
	}
	declType := g.resolveDeclType(name, item.Type, item.Value)
	if item.Name != nil {
		ident = g.identForDecl(item.Name, declType)
	} else {
		ident = &hir.Ident{
			Name:     "",
			Type:     declType,
			Location: source.Location{},
		}
	}

	return hir.DeclItem{
		Name:  ident,
		Type:  declType,
		Value: g.lowerOwnedTransferExpr(item.Value, declType, false),
	}
}

func (g *Generator) lowerTypeDecl(decl *ast.TypeDecl) *hir.TypeDecl {
	if decl == nil {
		return nil
	}
	var ident *hir.Ident
	if decl.Name != nil {
		ident = g.identForDecl(decl.Name, g.resolveDeclType(decl.Name.Name, decl.Type, nil))
	}
	return &hir.TypeDecl{
		Name:     ident,
		Type:     g.resolveTypeDecl(decl),
		Location: locFromNode(decl),
	}
}

func (g *Generator) lowerFuncDecl(decl *ast.FuncDecl) *hir.FuncDecl {
	if decl == nil {
		return nil
	}
	var ident *hir.Ident
	if decl.Name != nil {
		ident = g.identForDecl(decl.Name, g.resolveDeclType(decl.Name.Name, nil, nil))
	}

	var body *hir.Block
	g.withScope(decl.Scope, func() {
		body = g.lowerBlock(decl.Body)
	})

	return &hir.FuncDecl{
		Name:     ident,
		Type:     g.resolveFuncType(decl.Name, decl.Type),
		Body:     body,
		Location: locFromNode(decl),
	}
}

func (g *Generator) lowerMethodDecl(decl *ast.MethodDecl) *hir.MethodDecl {
	if decl == nil {
		return nil
	}

	var receiver *hir.Param
	if decl.Receiver != nil {
		receiver = g.lowerParam(decl.Receiver)
	}

	var ident *hir.Ident
	if decl.Name != nil {
		ident = g.identForDecl(decl.Name, g.resolveDeclType(decl.Name.Name, nil, nil))
	}

	var body *hir.Block
	g.withScope(decl.Scope, func() {
		body = g.lowerBlock(decl.Body)
	})

	return &hir.MethodDecl{
		Receiver: receiver,
		Name:     ident,
		Type:     g.resolveFuncType(decl.Name, decl.Type),
		Body:     body,
		Location: locFromNode(decl),
	}
}

func (g *Generator) lowerParam(field *ast.Field) *hir.Param {
	if field == nil {
		return nil
	}

	name := ""
	if field.Name != nil {
		name = field.Name.Name
	}

	return &hir.Param{
		Name:       name,
		Type:       g.typeFromNode(field.Type),
		IsVariadic: field.IsVariadic,
		IsMove:     field.IsMove,
		Location:   field.Location,
	}
}

func (g *Generator) lowerAssignStmt(stmt *ast.AssignStmt) *hir.AssignStmt {
	if stmt == nil {
		return nil
	}
	lhsType := g.exprType(stmt.Lhs)
	return &hir.AssignStmt{
		Lhs:      g.lowerExpr(stmt.Lhs),
		Rhs:      g.lowerOwnedTransferExpr(stmt.Rhs, lhsType, false),
		Op:       stmt.Op,
		Location: locFromNode(stmt),
	}
}

func (g *Generator) lowerReturnStmt(stmt *ast.ReturnStmt) *hir.ReturnStmt {
	if stmt == nil {
		return nil
	}
	return &hir.ReturnStmt{
		Result:   g.lowerOwnedTransferExpr(stmt.Result, nil, false),
		IsError:  stmt.IsError,
		Location: locFromNode(stmt),
	}
}

func (g *Generator) lowerImportStmt(stmt *ast.ImportStmt) *hir.ImportStmt {
	if stmt == nil {
		return nil
	}
	alias := ""
	if stmt.Alias != nil {
		alias = stmt.Alias.Name
	}
	path := ""
	if stmt.Path != nil {
		path = stmt.Path.Value
	}
	return &hir.ImportStmt{
		Path:           path,
		Alias:          alias,
		LocationOnDisk: stmt.LocationOnDisk,
		Location:       locFromNode(stmt),
	}
}

func (g *Generator) lowerExprStmt(stmt *ast.ExprStmt) *hir.ExprStmt {
	if stmt == nil {
		return nil
	}
	return &hir.ExprStmt{
		X:        g.lowerExpr(stmt.X),
		Location: locFromNode(stmt),
	}
}

func (g *Generator) lowerDeferStmt(stmt *ast.DeferStmt) *hir.DeferStmt {
	if stmt == nil {
		return nil
	}
	call, ok := g.lowerExpr(stmt.Call).(*hir.CallExpr)
	if !ok {
		// Should not happen if parser validated correctly
		return nil
	}
	return &hir.DeferStmt{
		Call:     call,
		Catch:    g.lowerCatchClause(stmt.Catch),
		Location: locFromNode(stmt),
	}
}

func (g *Generator) lowerBlock(block *ast.Block) *hir.Block {
	if block == nil {
		return nil
	}
	key := ""
	if block.Location.Start != nil {
		filePath := g.mod.FilePath
		if block.Location.Filename != nil && *block.Location.Filename != "" {
			filePath = *block.Location.Filename
		}
		key = narrowing.ScopeKeyFromLocation(filePath, block.Location.Start.Line, block.Location.Start.Column)
	}
	hirBlock := &hir.Block{
		Location:     locFromNode(block),
		NarrowingKey: key,
	}
	g.withScope(block.Scope, func() {
		nodes := make([]hir.Node, 0, len(block.Nodes))
		for _, node := range block.Nodes {
			if lowered := g.lowerNode(node); lowered != nil {
				nodes = append(nodes, lowered)
			}
		}
		hirBlock.Nodes = nodes
	})
	return hirBlock
}

func (g *Generator) lowerIfStmt(stmt *ast.IfStmt) *hir.IfStmt {
	if stmt == nil {
		return nil
	}
	hirStmt := &hir.IfStmt{
		Cond:     g.lowerExpr(stmt.Cond),
		Location: locFromNode(stmt),
	}
	g.withScope(stmt.Scope, func() {
		hirStmt.Body = g.lowerBlock(stmt.Body)
	})
	if stmt.Else != nil {
		hirStmt.Else = g.lowerNode(stmt.Else)
	}
	return hirStmt
}

func (g *Generator) lowerForStmt(stmt *ast.ForStmt) *hir.ForStmt {
	if stmt == nil {
		return nil
	}
	hirStmt := &hir.ForStmt{Location: locFromNode(stmt)}
	g.withScope(stmt.Scope, func() {
		hirStmt.Iterator = g.lowerNode(stmt.Iterator)
		hirStmt.Range = g.lowerExpr(stmt.Range)
		hirStmt.Body = g.lowerBlock(stmt.Body)
	})
	return hirStmt
}

func (g *Generator) lowerWhileStmt(stmt *ast.WhileStmt) *hir.WhileStmt {
	if stmt == nil {
		return nil
	}
	hirStmt := &hir.WhileStmt{Location: locFromNode(stmt)}
	g.withScope(stmt.Scope, func() {
		hirStmt.Cond = g.lowerExpr(stmt.Cond)
		hirStmt.Body = g.lowerBlock(stmt.Body)
	})
	return hirStmt
}

func (g *Generator) lowerMatchStmt(stmt *ast.MatchStmt) *hir.MatchStmt {
	if stmt == nil {
		return nil
	}
	hirStmt := &hir.MatchStmt{
		Expr:     g.lowerExpr(stmt.Expr),
		Location: locFromNode(stmt),
	}
	cases := make([]hir.CaseClause, 0, len(stmt.Cases))
	for _, clause := range stmt.Cases {
		if clause == nil {
			continue
		}
		cases = append(cases, hir.CaseClause{
			Pattern:  g.lowerExpr(clause.Pattern),
			Body:     g.lowerBlock(clause.Body),
			Location: clause.Location,
		})
	}
	hirStmt.Cases = cases
	return hirStmt
}

func (g *Generator) lowerCallExpr(expr *ast.CallExpr) *hir.CallExpr {
	if expr == nil {
		return nil
	}
	callArgs := expr.Args
	if g != nil && g.mod != nil {
		if resolved, ok := g.mod.CallArgs(expr); ok && len(resolved) > 0 {
			callArgs = resolved
		}
	}
	args := make([]hir.Expr, 0, len(callArgs))
	fnType := g.callExprFuncType(expr.Fun)
	for _, arg := range callArgs {
		paramType, isMove := g.callArgExpectedType(fnType, len(args))
		args = append(args, g.lowerOwnedTransferExpr(arg, paramType, isMove))
	}
	return &hir.CallExpr{
		Fun:      g.lowerExpr(expr.Fun),
		Args:     args,
		Catch:    g.lowerCatchClause(expr.Catch),
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
}

func (g *Generator) callExprFuncType(fun ast.Expression) *types.FunctionType {
	if fun == nil {
		return nil
	}
	if fn := unwrapFunctionType(g.exprType(fun)); fn != nil {
		return fn
	}
	if ident, ok := unwrapMoveSourceExpr(fun).(*ast.IdentifierExpr); ok {
		if sym := g.lookupSymbol(ident.Name); sym != nil {
			return unwrapFunctionType(sym.Type)
		}
	}
	return nil
}

func unwrapFunctionType(t types.SemType) *types.FunctionType {
	if t == nil || t.Equals(types.TypeUnknown) {
		return nil
	}
	if fn, ok := types.UnwrapType(t).(*types.FunctionType); ok {
		return fn
	}
	return nil
}

func (g *Generator) callArgExpectedType(fnType *types.FunctionType, index int) (types.SemType, bool) {
	if fnType == nil || index < 0 || len(fnType.Params) == 0 {
		return nil, false
	}
	if index < len(fnType.Params) {
		param := fnType.Params[index]
		return param.Type, param.IsMove
	}
	last := fnType.Params[len(fnType.Params)-1]
	if last.IsVariadic {
		return last.Type, last.IsMove
	}
	return nil, false
}

func isReferenceSemType(t types.SemType) bool {
	if t == nil {
		return false
	}
	_, ok := types.UnwrapType(t).(*types.ReferenceType)
	return ok
}

func moveSourceBaseExpr(expr ast.Expression) ast.Expression {
	expr = unwrapMoveSourceExpr(expr)
	for expr != nil {
		switch e := expr.(type) {
		case *ast.SelectorExpr:
			expr = unwrapMoveSourceExpr(e.X)
		case *ast.IndexExpr:
			expr = unwrapMoveSourceExpr(e.X)
		case *ast.DerefExpr:
			expr = unwrapMoveSourceExpr(e.X)
		default:
			return expr
		}
	}
	return nil
}

func (g *Generator) lowerOwnedTransferExpr(expr ast.Expression, expected types.SemType, forceMove bool) hir.Expr {
	if expr == nil {
		return nil
	}
	lowered := g.lowerExpr(expr)
	if lowered == nil {
		return nil
	}
	if !g.shouldImplicitMove(expr, expected, forceMove) {
		return lowered
	}

	loc := locFromNode(expr)
	start := source.Position{}
	end := source.Position{}
	if loc.Start != nil {
		start = *loc.Start
		end = *loc.Start
	}

	return &hir.UnaryExpr{
		Op:       tokens.NewToken(tokens.AT_TOKEN, "@", start, end),
		X:        lowered,
		Type:     g.exprType(expr),
		Location: loc,
	}
}

func (g *Generator) shouldImplicitMove(expr ast.Expression, expected types.SemType, forceMove bool) bool {
	if expr == nil {
		return false
	}
	if expected != nil {
		if _, isRef := types.UnwrapType(expected).(*types.ReferenceType); isRef {
			return false
		}
	}

	root := moveSourceBaseExpr(expr)
	if unary, ok := root.(*ast.UnaryExpr); ok && unary.Op.Kind == tokens.AT_TOKEN {
		return false
	}

	srcType := g.exprType(expr)
	if srcType == nil || srcType.Equals(types.TypeUnknown) {
		return false
	}
	if !forceMove && types.IsImplicitlyCopyableType(srcType) {
		return false
	}

	sym, ok := g.implicitMoveSourceSymbol(root)
	if !ok || sym == nil {
		return false
	}
	if sym.Kind != symbols.SymbolVariable && sym.Kind != symbols.SymbolParameter && sym.Kind != symbols.SymbolReceiver {
		return false
	}
	if sym.Kind == symbols.SymbolConstant || sym.IsReadonly {
		return false
	}
	if isReferenceSemType(sym.Type) {
		return false
	}
	if g != nil && g.mod != nil && g.mod.ModuleScope != nil && sym.DeclaredScope == g.mod.ModuleScope {
		return false
	}
	return true
}

func unwrapMoveSourceExpr(expr ast.Expression) ast.Expression {
	for expr != nil {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.CastExpr:
			expr = e.X
		default:
			return expr
		}
	}
	return nil
}

func (g *Generator) implicitMoveSourceSymbol(expr ast.Expression) (*symbols.Symbol, bool) {
	if expr == nil {
		return nil, false
	}
	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		sym := g.lookupSymbol(e.Name)
		if sym == nil {
			return nil, false
		}
		return sym, true
	case *ast.SelectorExpr:
		return g.implicitMoveSourceSymbol(e.X)
	case *ast.IndexExpr:
		return g.implicitMoveSourceSymbol(e.X)
	case *ast.DerefExpr:
		return g.implicitMoveSourceSymbol(e.X)
	case *ast.UnaryExpr:
		if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.MUT_TOKEN {
			return nil, false
		}
		return g.implicitMoveSourceSymbol(e.X)
	default:
		return nil, false
	}
}

// lowerPipeExpr transforms a pipe expression into a call expression
// value |> func(...) becomes func(..., value) or func(..., value, ...) with _ replaced
func (g *Generator) lowerPipeExpr(expr *ast.PipeExpr) *hir.CallExpr {
	if expr == nil {
		return nil
	}

	// Keep AST value form for ownership-aware lowering in argument positions.
	pipedValueExpr := expr.Value

	// If the right side is a call expression, apply placeholder semantics.
	if callExpr, ok := expr.Call.(*ast.CallExpr); ok {
		fnType := g.callExprFuncType(callExpr.Fun)
		if g != nil && g.mod != nil {
			if resolved, ok := g.mod.CallArgs(callExpr); ok && len(resolved) > 0 {
				args := make([]hir.Expr, 0, len(resolved))
				for i, arg := range resolved {
					paramType, isMove := g.callArgExpectedType(fnType, i)
					args = append(args, g.lowerOwnedTransferExpr(arg, paramType, isMove))
				}
				return &hir.CallExpr{
					Fun:      g.lowerExpr(callExpr.Fun),
					Args:     args,
					Catch:    g.lowerCatchClause(callExpr.Catch),
					Type:     g.exprType(expr),
					Location: locFromNode(expr),
				}
			}
		}

		// Transform arguments in AST form first, then lower with parameter-aware ownership rules.
		resolvedArgs := make([]ast.Expression, 0, len(callExpr.Args)+1)
		placeholderFound := false

		for _, arg := range callExpr.Args {
			// Check if this is a placeholder
			if ident, ok := arg.(*ast.IdentifierExpr); ok && ident.Name == "_" {
				resolvedArgs = append(resolvedArgs, pipedValueExpr)
				placeholderFound = true
			} else {
				resolvedArgs = append(resolvedArgs, arg)
			}
		}

		// If no placeholder, prepend the piped value as the first argument
		if !placeholderFound {
			resolvedArgs = append([]ast.Expression{pipedValueExpr}, resolvedArgs...)
		}

		transformedArgs := make([]hir.Expr, 0, len(resolvedArgs))
		for i, arg := range resolvedArgs {
			paramType, isMove := g.callArgExpectedType(fnType, i)
			transformedArgs = append(transformedArgs, g.lowerOwnedTransferExpr(arg, paramType, isMove))
		}

		// Create the transformed call expression
		return &hir.CallExpr{
			Fun:      g.lowerExpr(callExpr.Fun),
			Args:     transformedArgs,
			Catch:    g.lowerCatchClause(callExpr.Catch),
			Type:     g.exprType(expr),
			Location: locFromNode(expr),
		}
	}

	// Otherwise, treat the right side as a callable expression with a single argument.
	fnType := g.callExprFuncType(expr.Call)
	if g != nil && g.mod != nil {
		if resolved, ok := g.mod.PipeArgs(expr); ok && len(resolved) > 0 {
			args := make([]hir.Expr, 0, len(resolved))
			for i, arg := range resolved {
				paramType, isMove := g.callArgExpectedType(fnType, i)
				args = append(args, g.lowerOwnedTransferExpr(arg, paramType, isMove))
			}
			return &hir.CallExpr{
				Fun:      g.lowerExpr(expr.Call),
				Args:     args,
				Type:     g.exprType(expr),
				Location: locFromNode(expr),
			}
		}
	}

	firstParamType, firstParamMove := g.callArgExpectedType(fnType, 0)
	return &hir.CallExpr{
		Fun:      g.lowerExpr(expr.Call),
		Args:     []hir.Expr{g.lowerOwnedTransferExpr(pipedValueExpr, firstParamType, firstParamMove)},
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
}

func (g *Generator) lowerCatchClause(clause *ast.CatchClause) *hir.CatchClause {
	if clause == nil {
		return nil
	}
	var errIdent *hir.Ident
	if clause.ErrIdent != nil {
		if clause.Handler != nil && clause.Handler.Scope != nil {
			g.withScope(clause.Handler.Scope, func() {
				errIdent = g.identFromExpr(clause.ErrIdent)
			})
		} else {
			errIdent = g.identFromExpr(clause.ErrIdent)
		}
	}
	return &hir.CatchClause{
		ErrIdent: errIdent,
		Handler:  g.lowerBlock(clause.Handler),
		Fallback: g.lowerExpr(clause.Fallback),
		Location: locFromNode(clause),
	}
}

func (g *Generator) lowerSelectorExpr(expr *ast.SelectorExpr) *hir.SelectorExpr {
	if expr == nil {
		return nil
	}
	field := &hir.Ident{
		Name:     "",
		Type:     g.exprType(expr),
		Location: source.Location{},
	}
	if expr.Field != nil {
		field.Name = expr.Field.Name
		field.Location = locFromNode(expr.Field)
	}
	return &hir.SelectorExpr{
		X:        g.autoDerefExpr(expr, expr.X),
		Field:    field,
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
}

func (g *Generator) lowerScopeResolutionExpr(expr *ast.ScopeResolutionExpr) *hir.ScopeResolutionExpr {
	if expr == nil {
		return nil
	}
	selector := &hir.Ident{
		Name:     "",
		Type:     g.exprType(expr),
		Location: source.Location{},
	}
	if expr.Selector != nil {
		selector.Name = expr.Selector.Name
		selector.Location = locFromNode(expr.Selector)
	}
	return &hir.ScopeResolutionExpr{
		X:        g.lowerExpr(expr.X),
		Selector: selector,
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
}

func (g *Generator) lowerCastExpr(expr *ast.CastExpr) *hir.CastExpr {
	if expr == nil {
		return nil
	}
	targetType := g.typeFromNode(expr.Type)
	if targetType == nil || targetType.Equals(types.TypeUnknown) {
		targetType = g.exprType(expr)
	}
	return &hir.CastExpr{
		X:          g.lowerExpr(expr.X),
		TargetType: targetType,
		Type:       g.exprType(expr),
		Location:   locFromNode(expr),
	}
}

func (g *Generator) lowerCompositeLit(expr *ast.CompositeLit) *hir.CompositeLit {
	if expr == nil {
		return nil
	}
	litType := g.exprType(expr)
	unwrapped := types.UnwrapType(litType)
	elts := make([]hir.Expr, 0, len(expr.Elts))

	structFieldType := func(name string) types.SemType {
		st, ok := unwrapped.(*types.StructType)
		if !ok || st == nil {
			return nil
		}
		for _, field := range st.Fields {
			if field.Name == name {
				return field.Type
			}
		}
		return nil
	}

	for _, elt := range expr.Elts {
		switch e := elt.(type) {
		case *ast.KeyValueExpr:
			var valueExpected types.SemType
			switch t := unwrapped.(type) {
			case *types.MapType:
				valueExpected = t.Value
			case *types.StructType:
				if keyIdent, ok := e.Key.(*ast.IdentifierExpr); ok {
					valueExpected = structFieldType(keyIdent.Name)
				}
			}
			elts = append(elts, &hir.KeyValueExpr{
				// Never treat key position as an ownership-transfer site.
				Key:      g.lowerExpr(e.Key),
				Value:    g.lowerOwnedTransferExpr(e.Value, valueExpected, false),
				Location: locFromNode(e),
			})
		default:
			if arr, ok := unwrapped.(*types.ArrayType); ok && arr != nil {
				elts = append(elts, g.lowerOwnedTransferExpr(elt, arr.Element, false))
			} else {
				elts = append(elts, g.lowerOwnedTransferExpr(elt, nil, false))
			}
		}
	}
	return &hir.CompositeLit{
		Type:     litType,
		Elts:     elts,
		Location: locFromNode(expr),
	}
}

func (g *Generator) lowerFuncLit(expr *ast.FuncLit) *hir.FuncLit {
	if expr == nil {
		return nil
	}
	scope := expr.Scope
	if scope == nil {
		if g != nil && g.mod != nil {
			scope = table.NewSymbolTable(g.mod.CurrentScope)
			expr.Scope = scope
		}
	}
	hirLit := &hir.FuncLit{
		ID:       expr.ID.Name,
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
	g.withScope(scope, func() {
		hirLit.Body = g.lowerBlock(expr.Body)
	})
	hirLit.Captures = g.collectFuncLitCaptures(hirLit.Body, scope)
	return hirLit
}

func (g *Generator) collectFuncLitCaptures(body *hir.Block, scope ast.SymbolTable) []*hir.Ident {
	if body == nil || scope == nil {
		return nil
	}
	litScope, ok := scope.(*table.SymbolTable)
	if !ok || litScope == nil {
		return nil
	}

	seen := make(map[*symbols.Symbol]struct{})
	captures := make([]*hir.Ident, 0)

	var visitExpr func(hir.Expr)
	var visitNode func(hir.Node)

	visitExpr = func(expr hir.Expr) {
		if expr == nil {
			return
		}
		switch e := expr.(type) {
		case *hir.Ident:
			if e.Symbol == nil && e.Name != "" {
				if sym, ok := litScope.Lookup(e.Name); ok {
					e.Symbol = sym
				}
			}
			if g.shouldCaptureSymbol(e, litScope) {
				if _, ok := seen[e.Symbol]; !ok {
					seen[e.Symbol] = struct{}{}
					captures = append(captures, e)
				}
			}
		case *hir.FuncLit:
			for _, cap := range e.Captures {
				if cap == nil {
					continue
				}
				if cap.Symbol == nil && cap.Name != "" {
					if sym, ok := litScope.Lookup(cap.Name); ok {
						cap.Symbol = sym
					}
				}
				if g.shouldCaptureSymbol(cap, litScope) {
					if _, ok := seen[cap.Symbol]; !ok {
						seen[cap.Symbol] = struct{}{}
						captures = append(captures, cap)
					}
				}
			}
			return
		case *hir.BinaryExpr:
			visitExpr(e.X)
			visitExpr(e.Y)
		case *hir.UnaryExpr:
			visitExpr(e.X)
		case *hir.PrefixExpr:
			visitExpr(e.X)
		case *hir.PostfixExpr:
			visitExpr(e.X)
		case *hir.CallExpr:
			visitExpr(e.Fun)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
			if e.Catch != nil {
				if e.Catch.Handler != nil {
					for _, node := range e.Catch.Handler.Nodes {
						visitNode(node)
					}
				}
				visitExpr(e.Catch.Fallback)
			}
		case *hir.SelectorExpr:
			visitExpr(e.X)
		case *hir.DerefExpr:
			visitExpr(e.X)
		case *hir.ScopeResolutionExpr:
			visitExpr(e.X)
		case *hir.IndexExpr:
			visitExpr(e.X)
			visitExpr(e.Index)
		case *hir.CastExpr:
			visitExpr(e.X)
		case *hir.ParenExpr:
			visitExpr(e.X)
		case *hir.CompositeLit:
			for _, elt := range e.Elts {
				visitExpr(elt)
			}
		case *hir.KeyValueExpr:
			visitExpr(e.Value)
		case *hir.OptionalSome:
			visitExpr(e.Value)
		case *hir.OptionalIsSome:
			visitExpr(e.Value)
		case *hir.OptionalIsNone:
			visitExpr(e.Value)
		case *hir.OptionalUnwrap:
			visitExpr(e.Value)
			visitExpr(e.Default)
		case *hir.ResultOk:
			visitExpr(e.Value)
		case *hir.ResultErr:
			visitExpr(e.Value)
		case *hir.ResultUnwrap:
			visitExpr(e.Value)
			if e.Catch != nil {
				if e.Catch.Handler != nil {
					for _, node := range e.Catch.Handler.Nodes {
						visitNode(node)
					}
				}
				visitExpr(e.Catch.Fallback)
			}
		case *hir.CoalescingExpr:
			visitExpr(e.Cond)
			visitExpr(e.Default)
		case *hir.RangeExpr:
			visitExpr(e.Start)
			visitExpr(e.End)
			visitExpr(e.Incr)
		case *hir.ArrayLenExpr:
			visitExpr(e.X)
		case *hir.StringLenExpr:
			visitExpr(e.X)
		case *hir.MapIterInitExpr:
			visitExpr(e.Map)
		case *hir.MapIterNextExpr:
			visitExpr(e.Map)
			visitExpr(e.Iter)
		}
	}

	visitNode = func(node hir.Node) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *hir.Block:
			for _, stmt := range n.Nodes {
				visitNode(stmt)
			}
		case *hir.DeclStmt:
			visitNode(n.Decl)
		case *hir.VarDecl:
			for _, item := range n.Decls {
				visitExpr(item.Value)
			}
		case *hir.ConstDecl:
			for _, item := range n.Decls {
				visitExpr(item.Value)
			}
		case *hir.AssignStmt:
			visitExpr(n.Lhs)
			visitExpr(n.Rhs)
		case *hir.ReturnStmt:
			visitExpr(n.Result)
		case *hir.ExprStmt:
			visitExpr(n.X)
		case *hir.IfStmt:
			visitExpr(n.Cond)
			visitNode(n.Body)
			visitNode(n.Else)
		case *hir.ForStmt:
			visitNode(n.Iterator)
			visitExpr(n.Range)
			visitNode(n.Body)
		case *hir.WhileStmt:
			visitExpr(n.Cond)
			visitNode(n.Body)
		case *hir.MatchStmt:
			visitExpr(n.Expr)
			for _, clause := range n.Cases {
				if clause.Pattern != nil {
					visitExpr(clause.Pattern)
				}
				visitNode(clause.Body)
			}
		case *hir.DeferStmt:
			visitExpr(n.Call)
		}
	}

	visitNode(body)
	return captures
}

func (g *Generator) shouldCaptureSymbol(ident *hir.Ident, litScope *table.SymbolTable) bool {
	if ident == nil || ident.Symbol == nil || litScope == nil {
		return false
	}
	sym := ident.Symbol
	switch sym.Kind {
	case symbols.SymbolFunction, symbols.SymbolType:
		return false
	}
	if g.mod != nil && g.mod.ModuleScope != nil && sym.DeclaredScope == g.mod.ModuleScope {
		return false
	}
	if sym.DeclaredScope == nil {
		return false
	}
	declScope, ok := sym.DeclaredScope.(*table.SymbolTable)
	if !ok || declScope == nil {
		return false
	}
	for scope := declScope; scope != nil; scope = scope.Parent() {
		if scope == litScope {
			return false
		}
	}
	return true
}

func (g *Generator) lowerIdentExpr(expr *ast.IdentifierExpr) *hir.Ident {
	if expr == nil {
		return nil
	}
	return g.identFromExpr(expr)
}

func (g *Generator) identFromExpr(expr *ast.IdentifierExpr) *hir.Ident {
	if expr == nil {
		return nil
	}
	return &hir.Ident{
		Name:     expr.Name,
		Symbol:   g.lookupSymbol(expr.Name),
		Type:     g.exprType(expr),
		Location: locFromNode(expr),
	}
}

func (g *Generator) identForDecl(expr *ast.IdentifierExpr, declType types.SemType) *hir.Ident {
	if expr == nil {
		return nil
	}
	return &hir.Ident{
		Name:     expr.Name,
		Symbol:   g.lookupSymbol(expr.Name),
		Type:     declType,
		Location: locFromNode(expr),
	}
}

func (g *Generator) resolveDeclType(name string, explicitType ast.TypeNode, value ast.Expression) types.SemType {
	if name != "" && name != "_" {
		if sym := g.lookupSymbol(name); sym != nil && sym.Type != nil {
			return sym.Type
		}
	}
	if explicitType != nil {
		return g.typeFromNode(explicitType)
	}
	if value != nil {
		return g.exprType(value)
	}
	return types.TypeUnknown
}

func (g *Generator) resolveTypeDecl(decl *ast.TypeDecl) types.SemType {
	if decl == nil || decl.Name == nil {
		return types.TypeUnknown
	}
	if sym := g.lookupSymbol(decl.Name.Name); sym != nil && sym.Type != nil {
		return sym.Type
	}
	if decl.Type != nil {
		return g.typeFromNode(decl.Type)
	}
	return types.TypeUnknown
}

func (g *Generator) resolveFuncType(name *ast.IdentifierExpr, typeNode *ast.FuncType) *types.FunctionType {
	if name != nil {
		if sym := g.lookupSymbol(name.Name); sym != nil {
			if ft, ok := sym.Type.(*types.FunctionType); ok {
				return ft
			}
			if named, ok := sym.Type.(*types.NamedType); ok {
				if ft, ok := named.Underlying.(*types.FunctionType); ok {
					return ft
				}
			}
		}
	}
	if typeNode == nil {
		return nil
	}
	if sem := g.typeFromNode(typeNode); sem != nil {
		if ft, ok := sem.(*types.FunctionType); ok {
			return ft
		}
	}
	return nil
}

func (g *Generator) exprType(expr ast.Expression) types.SemType {
	if g == nil || g.ctx == nil || g.mod == nil || expr == nil {
		return types.TypeUnknown
	}
	return typechecker.ResolvedExprType(g.ctx, g.mod, expr)
}

func (g *Generator) typeFromNode(node ast.TypeNode) types.SemType {
	if g == nil || g.ctx == nil || g.mod == nil || node == nil {
		return types.TypeUnknown
	}
	return typechecker.TypeFromTypeNodeWithContext(g.ctx, g.mod, node)
}

func (g *Generator) lookupSymbol(name string) *symbols.Symbol {
	if g == nil || g.mod == nil || name == "" {
		return nil
	}
	scope := g.mod.CurrentScope
	if scope == nil {
		scope = g.mod.ModuleScope
	}
	if scope == nil {
		return nil
	}
	if sym, ok := scope.Lookup(name); ok {
		return sym
	}
	return nil
}

func (g *Generator) withScope(scope ast.SymbolTable, fn func()) {
	if fn == nil {
		return
	}
	if g == nil || g.mod == nil {
		fn()
		return
	}
	if scope == nil {
		fn()
		return
	}
	if tableScope, ok := scope.(*table.SymbolTable); ok {
		defer g.mod.EnterScope(tableScope)()
	}
	fn()
}

func (g *Generator) reportUnsupported(kind string, node ast.Node) {
	if g == nil || g.ctx == nil || node == nil {
		return
	}
	g.ctx.ReportError("hir/gen: unsupported "+kind, node.Loc())
}

func (g *Generator) lowerTypeExprToIdent(e *ast.TypeExpr) hir.Expr {
	// TypeExpr wraps a type node for use in expression context (e.g., 'is' operator)
	// For HIR, we convert it to an identifier that represents the type
	// The actual type checking is already done, so we just need a placeholder

	// Try to get a string representation of the type
	typeName := g.getTypeNodeName(e.Type)

	return &hir.Ident{
		Name:     typeName,
		Symbol:   nil, // This is a type name, not a variable
		Type:     g.exprType(e),
		Location: locFromNode(e),
	}
}

func (g *Generator) getTypeNodeName(typeNode ast.TypeNode) string {
	if typeNode == nil {
		return "unknown"
	}

	switch t := typeNode.(type) {
	case *ast.IdentifierExpr:
		return t.Name
	case *ast.ArrayType:
		elemName := g.getTypeNodeName(t.ElType)
		if t.Len != nil {
			return "[N]" + elemName
		}
		return "[]" + elemName
	case *ast.MapType:
		keyName := g.getTypeNodeName(t.Key)
		valName := g.getTypeNodeName(t.Value)
		return "map[" + keyName + "]" + valName
	case *ast.InterfaceType:
		if len(t.Methods) == 0 {
			return "interface{}"
		}
		return "interface"
	case *ast.OptionalType:
		baseName := g.getTypeNodeName(t.Base)
		return baseName + "?"
	case *ast.UnionType:
		return "union"
	case *ast.StructType:
		return "struct"
	default:
		return "type"
	}
}

func lowerLiteralKind(kind ast.LiteralKind) hir.LiteralKind {
	switch kind {
	case ast.INT:
		return hir.LiteralInt
	case ast.FLOAT:
		return hir.LiteralFloat
	case ast.IMAG:
		return hir.LiteralImag
	case ast.STRING:
		return hir.LiteralString
	case ast.BYTE:
		return hir.LiteralByte
	case ast.BOOL:
		return hir.LiteralBool
	case ast.NONE:
		return hir.LiteralNone
	default:
		return hir.LiteralInt
	}
}

func locFromNode(node ast.Node) source.Location {
	if node == nil || node.Loc() == nil {
		return source.Location{}
	}
	return *node.Loc()
}
