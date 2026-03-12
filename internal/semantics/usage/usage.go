package usage

import (
	"fmt"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/symbols"
)

type analyzer struct {
	ctx         *context.CompilerContext
	mod         *context.Module
	usedImports map[*binding.ImportBinding]bool
	usedSymbols map[*symbols.Symbol]int
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
	}
	a.collectUses()
	a.reportUnusedImports()
	a.reportUnusedModuleSymbols()
	a.reportUnusedFunctionSymbols()
	mod.Phase = phase.PhaseUsageAnalyzed
}

func (a *analyzer) collectUses() {
	if a == nil || a.mod == nil || a.mod.Bindings == nil {
		return
	}
	for node, resolution := range a.mod.Bindings.Nodes {
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
