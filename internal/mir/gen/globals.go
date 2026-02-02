package gen

import (
	"sort"

	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/mir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/types"
	ustrings "compiler/internal/utils/strings"
)

const initFuncName = "__ferret_init"

func globalSymbolName(importPath, name string) string {
	prefix := ustrings.ToIdentifier(importPath)
	if prefix == "" {
		return "g_" + name
	}
	return "g_" + prefix + "_" + name
}

func initFlagName(importPath string) string {
	prefix := ustrings.ToIdentifier(importPath)
	if prefix == "" {
		return "__ferret_init_done"
	}
	return "__ferret_init_done_" + prefix
}

func globalStorageType(sym *symbols.Symbol) types.SemType {
	if sym == nil || sym.Type == nil {
		return types.TypeUnknown
	}
	if sym.IsHeap {
		return types.NewReference(sym.Type)
	}
	return sym.Type
}

func (g *Generator) collectGlobals(hirMod *hir.Module) ([]hir.Node, []mir.Global) {
	if g == nil || g.mod == nil || hirMod == nil {
		return nil, nil
	}
	initNodes := make([]hir.Node, 0)
	globals := make([]mir.Global, 0)
	seen := make(map[string]struct{})

	addGlobal := func(sym *symbols.Symbol, loc source.Location) {
		if sym == nil {
			return
		}
		if g.mod.ModuleScope != nil && sym.DeclaredScope != g.mod.ModuleScope {
			return
		}
		if sym.Kind != symbols.SymbolVariable && sym.Kind != symbols.SymbolConstant {
			return
		}
		name := globalSymbolName(g.mod.ImportPath, sym.Name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		globals = append(globals, mir.Global{
			Name:     name,
			Type:     globalStorageType(sym),
			Location: loc,
		})
	}

	for _, item := range hirMod.Items {
		switch n := item.(type) {
		case *hir.VarDecl:
			initNodes = append(initNodes, n)
			for _, decl := range n.Decls {
				if decl.Name == nil {
					continue
				}
				addGlobal(decl.Name.Symbol, decl.Name.Location)
			}
		case *hir.ConstDecl:
			initNodes = append(initNodes, n)
			for _, decl := range n.Decls {
				if decl.Name == nil {
					continue
				}
				addGlobal(decl.Name.Symbol, decl.Name.Location)
			}
		}
	}

	flag := initFlagName(g.mod.ImportPath)
	if _, ok := seen[flag]; !ok {
		seen[flag] = struct{}{}
		globals = append(globals, mir.Global{
			Name:     flag,
			Type:     types.TypeBool,
			Location: hirMod.Location,
		})
	}

	return initNodes, globals
}

func (g *Generator) buildInitFunction(initNodes []hir.Node, loc source.Location) *mir.Function {
	if g == nil {
		return nil
	}
	fn := &mir.Function{
		Name:     initFuncName,
		Return:   types.TypeVoid,
		Location: loc,
	}
	builder := newFunctionBuilder(g, fn)
	builder.buildFuncBody(&hir.Block{Nodes: initNodes, Location: loc})
	g.injectInitPreamble(fn, loc)
	return fn
}

func (g *Generator) injectInitPreamble(fn *mir.Function, loc source.Location) {
	if g == nil || fn == nil || g.mod == nil || len(fn.Blocks) == 0 {
		return
	}

	body := fn.Blocks[0]
	initFlag := initFlagName(g.mod.ImportPath)

	preamble := make([]mir.Instr, 0)
	addrID := g.nextValueID()
	preamble = append(preamble, &mir.Const{
		Result:   addrID,
		Type:     types.NewReference(types.TypeBool),
		Value:    "$" + initFlag,
		Location: loc,
	})
	trueID := g.nextValueID()
	preamble = append(preamble, &mir.Const{
		Result:   trueID,
		Type:     types.TypeBool,
		Value:    "1",
		Location: loc,
	})
	preamble = append(preamble, &mir.Store{
		Addr:     addrID,
		Value:    trueID,
		Location: loc,
	})

	importCalls := g.initImportCalls(loc)
	preamble = append(preamble, importCalls...)

	prependInstrs(body, preamble)

	guard := &mir.Block{
		ID:       g.nextBlockID(),
		Name:     "init_guard",
		Location: loc,
	}
	guardAddr := g.nextValueID()
	guard.Instrs = append(guard.Instrs, &mir.Const{
		Result:   guardAddr,
		Type:     types.NewReference(types.TypeBool),
		Value:    "$" + initFlag,
		Location: loc,
	})
	loaded := g.nextValueID()
	guard.Instrs = append(guard.Instrs, &mir.Load{
		Result:   loaded,
		Addr:     guardAddr,
		Type:     types.TypeBool,
		Location: loc,
	})
	done := &mir.Block{
		ID:       g.nextBlockID(),
		Name:     "init_done",
		Location: loc,
		Term:     &mir.Return{HasValue: false, Location: loc},
	}
	guard.Term = &mir.CondBr{
		Cond:     loaded,
		Then:     done.ID,
		Else:     body.ID,
		Location: loc,
	}

	fn.Blocks = append([]*mir.Block{guard}, fn.Blocks...)
	fn.Blocks = append(fn.Blocks, done)
}

func (g *Generator) initImportCalls(loc source.Location) []mir.Instr {
	if g == nil || g.mod == nil {
		return nil
	}
	aliases := make([]string, 0, len(g.mod.Imports))
	for path, imp := range g.mod.Imports {
		if imp == nil || path == "" {
			continue
		}
		if path == context_v2.GlobalModuleImport {
			continue
		}
		if g.ctx != nil {
			if imported, ok := g.ctx.GetModule(path); ok && imported != nil {
				if imported.Type == context_v2.ModuleBuiltin && imported.AST != nil && len(imported.AST.Nodes) == 0 {
					continue
				}
			}
		}
		if imp.Alias != "" {
			aliases = append(aliases, imp.Alias)
		}
	}
	sort.Strings(aliases)

	calls := make([]mir.Instr, 0, len(aliases))
	for _, alias := range aliases {
		calls = append(calls, &mir.Call{
			Result:   mir.InvalidValue,
			Target:   alias + "::" + initFuncName,
			Args:     nil,
			Type:     types.TypeVoid,
			Location: loc,
		})
	}
	return calls
}

func prependInstrs(block *mir.Block, instrs []mir.Instr) {
	if block == nil || len(instrs) == 0 {
		return
	}
	idx := 0
	for idx < len(block.Instrs) {
		if _, ok := block.Instrs[idx].(*mir.Phi); ok {
			idx++
			continue
		}
		break
	}
	next := make([]mir.Instr, 0, len(block.Instrs)+len(instrs))
	next = append(next, block.Instrs[:idx]...)
	next = append(next, instrs...)
	next = append(next, block.Instrs[idx:]...)
	block.Instrs = next
}

func (g *Generator) injectEntryInitCall(mirMod *mir.Module) {
	if g == nil || g.ctx == nil || g.mod == nil || mirMod == nil {
		return
	}
	if g.mod.ImportPath != g.ctx.EntryModule {
		return
	}
	var mainFn *mir.Function
	for _, fn := range mirMod.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil || len(mainFn.Blocks) == 0 {
		return
	}
	call := &mir.Call{
		Result:   mir.InvalidValue,
		Target:   initFuncName,
		Args:     nil,
		Type:     types.TypeVoid,
		Location: mainFn.Location,
	}
	prependInstrs(mainFn.Blocks[0], []mir.Instr{call})
}
