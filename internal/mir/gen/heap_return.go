package gen

import (
	"strings"

	"compiler/internal/hir"
	"compiler/internal/types"
)

func (g *Generator) loadHeapReturns() {
	if g == nil || g.mod == nil {
		return
	}
	g.heapReturns = hir.HeapReturnMapFromModule(g.mod)
	if g.heapReturns == nil {
		g.heapReturns = make(map[string]types.SemType)
	}
}

func (g *Generator) heapReturnType(name string) (types.SemType, bool) {
	if g == nil || g.heapReturns == nil || name == "" {
		return nil, false
	}
	typ, ok := g.heapReturns[name]
	if ok {
		return typ, true
	}
	if !strings.Contains(name, "::") || g.mod == nil || g.ctx == nil {
		return nil, false
	}
	parts := strings.Split(name, "::")
	if len(parts) < 2 {
		return nil, false
	}
	moduleAlias := strings.Join(parts[:len(parts)-1], "::")
	funcName := parts[len(parts)-1]
	importPath := ""
	if g.mod.ImportAliasMap != nil {
		importPath = g.mod.ImportAliasMap[moduleAlias]
	}
	if importPath == "" {
		return nil, false
	}
	imported, ok := g.ctx.GetModule(importPath)
	if !ok || imported == nil {
		return nil, false
	}
	heapReturns := hir.HeapReturnMapFromModule(imported)
	if heapReturns == nil {
		return nil, false
	}
	typ, ok = heapReturns[funcName]
	return typ, ok
}

func (g *Generator) isHeapReturnFunc(name string) bool {
	_, ok := g.heapReturnType(name)
	return ok
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
