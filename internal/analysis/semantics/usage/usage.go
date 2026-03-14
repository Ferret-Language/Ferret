package usage

import (
	"fmt"
	"reflect"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/core/phase"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
)

type analyzer struct {
	ctx         *context.CompilerContext
	mod         *context.Module
	usedImports map[*binding.ImportBinding]bool
	usedSymbols map[*symbols.Symbol]int
	declNodes   map[ast.Node]struct{}
}

func AnalyzeModules(ctx *context.CompilerContext, mods []*context.Module) {
	if ctx == nil {
		return
	}
	for _, mod := range mods {
		AnalyzeModule(ctx, mod)
	}
}

func AnalyzeModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil || mod.Bindings == nil || mod.ModuleScope == nil {
		return
	}
	a := &analyzer{
		ctx:         ctx,
		mod:         mod,
		usedImports: make(map[*binding.ImportBinding]bool),
		usedSymbols: make(map[*symbols.Symbol]int),
		declNodes:   make(map[ast.Node]struct{}),
	}
	a.collectDeclNodes()
	a.collectUses()
	a.reportUnusedImports()
	a.reportUnusedModuleSymbols()
	a.reportUnusedFunctionSymbols()
	mod.Phase = phase.PhaseUsageAnalyzed
}

func (a *analyzer) collectDeclNodes() {
	if a == nil || a.mod == nil || a.mod.AST == nil {
		return
	}
	for _, decl := range a.mod.AST.Decls {
		a.collectDeclNodesDecl(decl)
	}
}

func (a *analyzer) markDeclNode(node ast.Node) {
	if a == nil || node == nil {
		return
	}
	a.declNodes[node] = struct{}{}
}

func (a *analyzer) collectDeclNodesDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.LetDecl:
		if d == nil {
			return
		}
		a.markDeclNode(d.Name)
		a.collectDeclNodesExpr(d.Value)
	case *ast.ConstDecl:
		if d == nil {
			return
		}
		a.markDeclNode(d.Name)
		a.collectDeclNodesExpr(d.Value)
	case *ast.TypeDecl:
		if d == nil {
			return
		}
		a.markDeclNode(d.Name)
	case *ast.FuncDecl:
		if d == nil {
			return
		}
		a.markDeclNode(d.Name)
		if d.Receiver != nil {
			a.markDeclNode(d.Receiver.Name)
		}
		for _, p := range d.Params {
			a.markDeclNode(p.Name)
		}
		a.collectDeclNodesStmt(d.Body)
	}
}

func (a *analyzer) collectDeclNodesStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		if s == nil {
			return
		}
		for _, child := range s.Stmts {
			a.collectDeclNodesStmt(child)
		}
	case *ast.LetStmt:
		if s == nil {
			return
		}
		a.markDeclNode(s.Name)
		a.collectDeclNodesExpr(s.Value)
	case *ast.ConstStmt:
		if s == nil {
			return
		}
		a.markDeclNode(s.Name)
		a.collectDeclNodesExpr(s.Value)
	case *ast.ReturnStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
	case *ast.ExprStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
	case *ast.AssignStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Left)
		a.collectDeclNodesExpr(s.Right)
	case *ast.IfStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Cond)
		a.collectDeclNodesStmt(s.Then)
		a.collectDeclNodesStmt(s.Else)
	case *ast.MatchStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			if arm.TypePattern != nil {
				// no decl binders
			} else if !arm.Wildcard {
				a.collectDeclNodesExpr(arm.Pattern)
			}
			a.collectDeclNodesStmt(arm.Body)
		}
	case *ast.WhileStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Cond)
		a.collectDeclNodesStmt(s.Body)
	case *ast.ForStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Iterable)
		a.markDeclNode(s.Index)
		a.markDeclNode(s.Value)
		a.collectDeclNodesStmt(s.Body)
	case *ast.LabelStmt:
		if s == nil {
			return
		}
		// Labels are tracked separately, but don't count them as uses.
		a.markDeclNode(s.Name)
		a.collectDeclNodesStmt(s.Stmt)
	case *ast.BreakStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Label)
	case *ast.ContinueStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Label)
	case *ast.DeferStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesStmt(s.Body)
	case *ast.ReleaseStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
	case *ast.PanicStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
	case *ast.LockStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesExpr(s.Value)
		a.markDeclNode(s.Name)
		a.collectDeclNodesStmt(s.Body)
	case *ast.UnsafeStmt:
		if s == nil {
			return
		}
		a.collectDeclNodesStmt(s.Body)
	}
}

func (a *analyzer) collectDeclNodesExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	v := reflect.ValueOf(expr)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	switch e := expr.(type) {
	case *ast.PrefixExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Right)
	case *ast.BinaryExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
		a.collectDeclNodesExpr(e.Right)
	case *ast.PostfixExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
	case *ast.CallExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Callee)
		for _, arg := range e.Args {
			a.collectDeclNodesExpr(arg)
		}
	case *ast.SelectorExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
	case *ast.CastExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
	case *ast.IsExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
	case *ast.MatchExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Value)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			if arm.TypePattern != nil {
				// no decl binders
			} else if !arm.Wildcard {
				a.collectDeclNodesExpr(arm.Pattern)
			}
			a.collectDeclNodesStmt(arm.Body)
		}
	case *ast.CatchExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
		a.collectDeclNodesExpr(e.Fallback)
		a.markDeclNode(e.Payload)
		a.collectDeclNodesStmt(e.Handler)
	case *ast.CompositeLit:
		if e == nil {
			return
		}
		for _, item := range e.Items {
			a.collectDeclNodesExpr(item.Value)
		}
	case *ast.IndexExpr:
		if e == nil {
			return
		}
		a.collectDeclNodesExpr(e.Left)
		a.collectDeclNodesExpr(e.Index)
	}
}

func (a *analyzer) collectUses() {
	if a == nil || a.mod == nil || a.mod.Bindings == nil {
		return
	}
	for node, resolution := range a.mod.Bindings.Nodes {
		if _, ok := a.declNodes[node]; ok {
			continue
		}
		if resolution == nil {
			continue
		}
		if resolution.Symbol != nil {
			a.usedSymbols[resolution.Symbol]++
		}
		path := nodePath(node)
		if len(path) < 2 {
			continue
		}
		if imp := matchImport(a.mod.Bindings.Imports, path); imp != nil {
			a.usedImports[imp] = true
		}
	}
}

func (a *analyzer) reportUnusedImports() {
	if a == nil || a.mod == nil || a.mod.Bindings == nil {
		return
	}
	for _, imp := range a.mod.Bindings.Imports {
		if imp == nil || a.usedImports[imp] {
			continue
		}
		msg := fmt.Sprintf("unused import %q", imp.ImportPath)
		a.ctx.Diagnostics.Add(
			diagnostics.NewWarning(msg).
				WithCode(diagnostics.WarnUnusedImport).
				WithPrimaryLabel(&imp.Location, "import is never used"),
		)
	}
}

func (a *analyzer) reportUnusedModuleSymbols() {
	if a == nil || a.mod == nil || a.mod.ModuleScope == nil {
		return
	}
	for _, sym := range a.mod.ModuleScope.Symbols() {
		if !shouldWarnOnUnusedModuleSymbol(a.mod, sym) {
			continue
		}
		if a.usedSymbols[sym] > 0 {
			continue
		}
		msg, code, label := unusedSymbolDiagnostic(sym)
		a.ctx.Diagnostics.Add(
			diagnostics.NewWarning(msg).
				WithCode(code).
				WithPrimaryLabel(&sym.Location, label),
		)
	}
}

func (a *analyzer) reportUnusedFunctionSymbols() {
	if a == nil || a.mod == nil || a.mod.Bindings == nil {
		return
	}
	for fn, locals := range a.mod.Bindings.FunctionLocals {
		if !a.shouldWarnInsideFunction(fn) {
			continue
		}
		for _, sym := range locals {
			if !shouldWarnOnUnusedFunctionSymbol(sym) {
				continue
			}
			if a.usedSymbols[sym] > 0 {
				continue
			}
			msg, code, label := unusedFunctionSymbolDiagnostic(sym)
			a.ctx.Diagnostics.Add(
				diagnostics.NewWarning(msg).
					WithCode(code).
					WithPrimaryLabel(&sym.Location, label),
			)
		}
	}
}

func shouldWarnOnUnusedModuleSymbol(mod *context.Module, sym *symbols.Symbol) bool {
	if mod == nil || sym == nil || sym.Exported {
		return false
	}
	if sym.Name == "_" {
		return false
	}
	if sym.Kind == symbols.SymbolFunc && sym.Name == "main" && mod.IsEntry {
		return false
	}
	switch node := sym.Node.(type) {
	case *ast.FuncDecl:
		if node.IsBuiltin || node.IsExtern {
			return false
		}
		return sym.Kind == symbols.SymbolFunc
	case *ast.TypeDecl:
		return sym.Kind == symbols.SymbolType
	case *ast.LetDecl, *ast.ConstDecl:
		return sym.Kind == symbols.SymbolVar || sym.Kind == symbols.SymbolConst
	default:
		return false
	}
}

func shouldWarnOnUnusedFunctionSymbol(sym *symbols.Symbol) bool {
	if sym == nil || sym.Name == "_" {
		return false
	}
	switch sym.Kind {
	case symbols.SymbolParam, symbols.SymbolVar, symbols.SymbolConst:
		return true
	default:
		return false
	}
}

func unusedSymbolDiagnostic(sym *symbols.Symbol) (msg, code, label string) {
	switch sym.Kind {
	case symbols.SymbolFunc:
		return fmt.Sprintf("unused private function %q", sym.Name), diagnostics.WarnUnusedPrivateFunction, "private function is never referenced"
	case symbols.SymbolType:
		return fmt.Sprintf("unused private type %q", sym.Name), diagnostics.WarnUnusedPrivateType, "private type is never referenced"
	default:
		return fmt.Sprintf("unused private module binding %q", sym.Name), diagnostics.WarnUnusedPrivateBinding, "private module binding is never referenced"
	}
}

func unusedFunctionSymbolDiagnostic(sym *symbols.Symbol) (msg, code, label string) {
	switch sym.Kind {
	case symbols.SymbolParam:
		return fmt.Sprintf("unused parameter %q", sym.Name), diagnostics.WarnUnusedParameter, "parameter is never used"
	default:
		return fmt.Sprintf("unused local %q", sym.Name), diagnostics.WarnUnusedLocal, "local is never used"
	}
}

func (a *analyzer) shouldWarnInsideFunction(fn *ast.FuncDecl) bool {
	if a == nil || fn == nil {
		return false
	}
	if fn.IsBuiltin || fn.IsExtern {
		return false
	}
	sym := a.mod.Bindings.FunctionSymbols[fn]
	if sym == nil {
		return true
	}
	if sym.Exported {
		return true
	}
	if sym.Kind == symbols.SymbolFunc && sym.Name == "main" && a.mod.IsEntry {
		return true
	}
	if fn.IsConstructor || fn.IsDestructor {
		return true
	}
	return a.usedSymbols[sym] > 0
}

func nodePath(node ast.Node) []string {
	switch n := node.(type) {
	case *ast.Ident:
		return n.Path
	case *ast.NamedType:
		return n.Path
	default:
		return nil
	}
}

func matchImport(imports []*binding.ImportBinding, path []string) *binding.ImportBinding {
	var best *binding.ImportBinding
	bestLen := 0
	for _, imp := range imports {
		if imp == nil || len(imp.Segments) == 0 || len(imp.Segments) > len(path) {
			continue
		}
		matched := true
		for i, seg := range imp.Segments {
			if path[i] != seg {
				matched = false
				break
			}
		}
		if matched && len(imp.Segments) > bestLen {
			best = imp
			bestLen = len(imp.Segments)
		}
	}
	return best
}
