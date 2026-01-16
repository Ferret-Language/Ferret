package gen

import (
	"compiler/internal/hir"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

func (g *Generator) heapReturnType(name string) (types.SemType, bool) {
	if g == nil || g.heapReturns == nil || name == "" {
		return nil, false
	}
	typ, ok := g.heapReturns[name]
	return typ, ok
}

func (g *Generator) isHeapReturnFunc(name string) bool {
	_, ok := g.heapReturnType(name)
	return ok
}

func (g *Generator) collectHeapReturns(mod *hir.Module) {
	if g == nil || mod == nil {
		return
	}

	fnReturns := make(map[string][]hir.Expr)
	fnRetTypes := make(map[string]types.SemType)

	for _, item := range mod.Items {
		switch n := item.(type) {
		case *hir.FuncDecl:
			if n == nil || n.Name == nil || n.Body == nil {
				continue
			}
			name := n.Name.Name
			fnRetTypes[name] = g.returnType(n.Type)
			var returns []hir.Expr
			collectReturnExprs(n.Body, &returns)
			fnReturns[name] = returns
		case *hir.MethodDecl:
			if n == nil || n.Name == nil || n.Body == nil {
				continue
			}
			name := g.methodName(n)
			fnRetTypes[name] = g.returnType(n.Type)
			var returns []hir.Expr
			collectReturnExprs(n.Body, &returns)
			fnReturns[name] = returns
		}
	}

	heapReturns := make(map[string]types.SemType)
	for name, exprs := range fnReturns {
		for _, expr := range exprs {
			if isHeapMoveReturn(expr) {
				heapReturns[name] = fnRetTypes[name]
				break
			}
		}
	}

	changed := true
	for changed {
		changed = false
		for name, exprs := range fnReturns {
			if _, ok := heapReturns[name]; ok {
				continue
			}
			for _, expr := range exprs {
				if callee, ok := heapReturnCallTarget(expr); ok {
					if _, ok := heapReturns[callee]; ok {
						heapReturns[name] = fnRetTypes[name]
						changed = true
						break
					}
				}
			}
		}
	}

	g.heapReturns = heapReturns
}

func collectReturnExprs(node hir.Node, out *[]hir.Expr) {
	if node == nil || out == nil {
		return
	}

	switch n := node.(type) {
	case *hir.Block:
		for _, child := range n.Nodes {
			collectReturnExprs(child, out)
		}
	case *hir.ReturnStmt:
		*out = append(*out, n.Result)
	case *hir.DeclStmt:
		collectReturnExprs(n.Decl, out)
	case *hir.VarDecl:
		return
	case *hir.ConstDecl:
		return
	case *hir.TypeDecl:
		return
	case *hir.AssignStmt:
		return
	case *hir.ExprStmt:
		if call, ok := n.X.(*hir.CallExpr); ok && call.Catch != nil {
			collectReturnExprs(call.Catch.Handler, out)
		}
	case *hir.IfStmt:
		collectReturnExprs(n.Body, out)
		if n.Else != nil {
			collectReturnExprs(n.Else, out)
		}
	case *hir.ForStmt:
		collectReturnExprs(n.Iterator, out)
		collectReturnExprs(n.Body, out)
	case *hir.WhileStmt:
		collectReturnExprs(n.Body, out)
	case *hir.MatchStmt:
		for _, clause := range n.Cases {
			collectReturnExprs(clause.Body, out)
		}
	case *hir.DeferStmt:
		if n.Catch != nil {
			collectReturnExprs(n.Catch.Handler, out)
		}
	}
}

func isHeapMoveReturn(expr hir.Expr) bool {
	expr = unwrapParenExpr(expr)
	unary, ok := expr.(*hir.UnaryExpr)
	if !ok || unary.Op.Kind != tokens.AT_TOKEN {
		return false
	}
	ident, ok := unary.X.(*hir.Ident)
	if !ok || ident.Symbol == nil {
		return false
	}
	return ident.Symbol.IsHeap
}

func heapReturnCallTarget(expr hir.Expr) (string, bool) {
	expr = unwrapParenExpr(expr)
	call, ok := expr.(*hir.CallExpr)
	if !ok || call == nil {
		return "", false
	}
	return callTargetName(call.Fun)
}

func callTargetName(expr hir.Expr) (string, bool) {
	if expr == nil {
		return "", false
	}
	switch e := expr.(type) {
	case *hir.Ident:
		return e.Name, e.Name != ""
	case *hir.ScopeResolutionExpr:
		left, ok := callTargetName(e.X)
		if !ok || e.Selector == nil || e.Selector.Name == "" {
			return "", false
		}
		return left + "::" + e.Selector.Name, true
	default:
		return "", false
	}
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
