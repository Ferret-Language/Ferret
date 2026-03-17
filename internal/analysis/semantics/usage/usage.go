package usage

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"fmt"
	"reflect"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/frontend/ast"
)

type analyzer struct {
	ctx         *context.CompilerContext
	mod         *context.Module
	usedImports map[*binding.ImportBinding]bool
	usedSymbols map[*symbols.Symbol]int
	written     map[*symbols.Symbol]int
	declNodes   map[ast.Node]struct{}
	writeNodes  map[ast.Node]struct{}
	readWrites  map[ast.Node]struct{}
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
		written:     make(map[*symbols.Symbol]int),
		declNodes:   make(map[ast.Node]struct{}),
		writeNodes:  make(map[ast.Node]struct{}),
		readWrites:  make(map[ast.Node]struct{}),
	}
	a.collectDeclNodes()
	a.collectUses()
	a.reportUnusedImports()
	a.reportUnusedModuleSymbols()
	a.reportUnusedFunctionSymbols()
	a.reportNeverModifiedMutableBindings()
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
		a.markWriteTarget(s.Left)
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

func (a *analyzer) markWriteTarget(expr ast.Expr) {
	a.markTarget(expr, false)
}

func (a *analyzer) markReadWriteTarget(expr ast.Expr) {
	a.markTarget(expr, true)
}

func (a *analyzer) markTarget(expr ast.Expr, readWrite bool) {
	if a == nil || expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		ident := e
		a.writeNodes[ident] = struct{}{}
		if readWrite {
			a.readWrites[ident] = struct{}{}
		}
	case *ast.SelectorExpr:
		a.markTarget(e.Left, readWrite)
	case *ast.IndexExpr:
		a.markTarget(e.Left, readWrite)
	case *ast.PrefixExpr:
		switch e.Op {
		case "*", "&", "&mut":
			a.markTarget(e.Right, readWrite)
		}
	case *ast.CastExpr:
		a.markTarget(e.Left, readWrite)
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
		a.markCallWrites(e)
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

func (a *analyzer) markCallWrites(call *ast.CallExpr) {
	if a == nil || call == nil || a.mod == nil || a.mod.Types == nil {
		return
	}
	if receiverType, ok := a.mod.Types.LookupMethodReceiver(call); ok {
		if ref, ok := receiverType.(*typeinfo.RefType); ok && ref.Mutable {
			if sel, ok := call.Callee.(*ast.SelectorExpr); ok {
				a.markReadWriteTarget(sel.Left)
			}
		}
	}
	fnType, ok := a.mod.Types.Nodes[call.Callee].(*typeinfo.FuncType)
	if !ok || fnType == nil {
		return
	}
	for i, arg := range call.Args {
		if i < len(fnType.MutParams) && fnType.MutParams[i] {
			a.markReadWriteTarget(arg)
		}
	}
}

func (a *analyzer) collectUses() {
	if a == nil || a.mod == nil || a.mod.Bindings == nil {
		return
	}
	for node, resolution := range a.mod.Bindings.Nodes {
		if _, ok := a.writeNodes[node]; ok {
			if resolution != nil && resolution.Symbol != nil {
				a.written[resolution.Symbol]++
			}
			if _, ok := a.readWrites[node]; !ok {
				continue
			}
		}
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

func (a *analyzer) reportNeverModifiedMutableBindings() {
	if a == nil || a.mod == nil {
		return
	}
	for _, decl := range a.mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.LetDecl:
			if d == nil || !d.IsMut {
				continue
			}
			a.reportNeverModifiedDecl(d.Name)
		case *ast.FuncDecl:
			if d == nil || d.Body == nil || !a.shouldWarnInsideFunction(d) {
				continue
			}
			a.reportNeverModifiedStmt(d.Body)
		}
	}
}

func (a *analyzer) reportNeverModifiedStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, child := range s.Stmts {
			a.reportNeverModifiedStmt(child)
		}
	case *ast.LetStmt:
		if s != nil && s.IsMut {
			a.reportNeverModifiedDecl(s.Name)
		}
	case *ast.IfStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Then)
			a.reportNeverModifiedStmt(s.Else)
		}
	case *ast.MatchStmt:
		if s != nil {
			for _, arm := range s.Arms {
				if arm != nil {
					a.reportNeverModifiedStmt(arm.Body)
				}
			}
		}
	case *ast.WhileStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Body)
		}
	case *ast.ForStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Body)
		}
	case *ast.LabelStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Stmt)
		}
	case *ast.DeferStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Body)
		}
	case *ast.LockStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Body)
		}
	case *ast.UnsafeStmt:
		if s != nil {
			a.reportNeverModifiedStmt(s.Body)
		}
	}
}

func (a *analyzer) reportNeverModifiedDecl(node ast.Node) {
	if a == nil || node == nil {
		return
	}
	resolution := a.mod.Bindings.Nodes[node]
	if resolution == nil || resolution.Symbol == nil {
		return
	}
	sym := resolution.Symbol
	if sym.Name == "_" || a.usedSymbols[sym] == 0 || a.written[sym] > 0 {
		return
	}
	a.ctx.Diagnostics.Add(
		diagnostics.NewWarning(fmt.Sprintf("%q is never modified", sym.Name)).
			WithCode(diagnostics.WarnUnmodifiedMutable).
			WithPrimaryLabel(&sym.Location, "remove `mut` from this binding"),
	)
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
	if sym.Kind == symbols.SymbolParam && sym.Name == "self" {
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
