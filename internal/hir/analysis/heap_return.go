package analysis

import (
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

func markHeapReturnFunctions(_ *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	if mod == nil || hirMod == nil {
		return
	}

	fnReturns := make(map[string][]hir.Expr)
	fnRetTypes := make(map[string]types.SemType)

	for _, item := range hirMod.Items {
		switch n := item.(type) {
		case *hir.FuncDecl:
			if n == nil || n.Name == nil || n.Body == nil {
				continue
			}
			name := n.Name.Name
			retType := types.TypeUnknown
			if n.Type != nil && n.Type.Return != nil {
				retType = n.Type.Return
			}
			fnRetTypes[name] = retType
			var returns []hir.Expr
			collectReturnExprs(n.Body, &returns)
			fnReturns[name] = returns
		case *hir.MethodDecl:
			if n == nil || n.Name == nil || n.Body == nil {
				continue
			}
			name := methodName(n)
			retType := types.TypeUnknown
			if n.Type != nil && n.Type.Return != nil {
				retType = n.Type.Return
			}
			fnRetTypes[name] = retType
			var returns []hir.Expr
			collectReturnExprs(n.Body, &returns)
			fnReturns[name] = returns
		}
	}

	heapReturns := make(map[string]types.SemType)
	for name, exprs := range fnReturns {
		if _, alreadyHeapTyped := types.UnwrapType(fnRetTypes[name]).(*types.HeapType); alreadyHeapTyped {
			continue
		}
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
			if _, alreadyHeapTyped := types.UnwrapType(fnRetTypes[name]).(*types.HeapType); alreadyHeapTyped {
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

	hir.StoreHeapReturnMap(mod, heapReturns)
}

func collectReturnExprs(node hir.Node, out *[]hir.Expr) {
	if node == nil || out == nil {
		return
	}

	switch n := node.(type) {
	case *hir.Block:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			collectReturnExprs(child, out)
		}
	case *hir.ReturnStmt:
		if n == nil {
			return
		}
		*out = append(*out, n.Result)
	case *hir.DeclStmt:
		if n == nil {
			return
		}
		collectReturnExprs(n.Decl, out)
	case *hir.VarDecl:
		if n == nil {
			return
		}
		return
	case *hir.ConstDecl:
		if n == nil {
			return
		}
		return
	case *hir.TypeDecl:
		if n == nil {
			return
		}
		return
	case *hir.AssignStmt:
		if n == nil {
			return
		}
		return
	case *hir.ExprStmt:
		if n == nil {
			return
		}
		if call, ok := n.X.(*hir.CallExpr); ok && call.Catch != nil {
			collectReturnExprs(call.Catch.Handler, out)
		}
	case *hir.IfStmt:
		if n == nil {
			return
		}
		collectReturnExprs(n.Body, out)
		if n.Else != nil {
			collectReturnExprs(n.Else, out)
		}
	case *hir.ForStmt:
		if n == nil {
			return
		}
		collectReturnExprs(n.Iterator, out)
		collectReturnExprs(n.Body, out)
	case *hir.WhileStmt:
		if n == nil {
			return
		}
		collectReturnExprs(n.Body, out)
	case *hir.MatchStmt:
		if n == nil {
			return
		}
		for _, clause := range n.Cases {
			collectReturnExprs(clause.Body, out)
		}
	case *hir.DeferStmt:
		if n == nil {
			return
		}
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
	if _, ok := types.UnwrapType(ident.Symbol.Type).(*types.HeapType); ok {
		return true
	}
	// Backward-compatible fallback while migrating old symbol metadata.
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

func methodName(decl *hir.MethodDecl) string {
	if decl == nil || decl.Name == nil {
		return ""
	}
	recvName := receiverTypeName(decl.Receiver)
	if recvName == "" {
		return decl.Name.Name
	}
	return recvName + "_" + decl.Name.Name
}

func receiverTypeName(recv *hir.Param) string {
	if recv == nil {
		return ""
	}
	recvType := recv.Type
	if ref, ok := recvType.(*types.ReferenceType); ok {
		recvType = ref.Inner
	}
	if named, ok := recvType.(*types.NamedType); ok {
		return named.Name
	}
	return ""
}
