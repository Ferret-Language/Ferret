package pipeline

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"compiler/internal/analysis/attrfilter"
	cfganalysis "compiler/internal/analysis/cfg/analysis"
	layoutanalysis "compiler/internal/analysis/layout/analysis"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/collector"
	"compiler/internal/analysis/semantics/ownership"
	"compiler/internal/analysis/semantics/resolver"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typechecker"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/analysis/semantics/usage"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/ir/hir"
	"compiler/internal/ir/mir"
)

// Pipeline coordinates the compilation process.
//
// Parsing is parallelised: every reachable module is lexed, parsed, and
// symbol-collected concurrently.  Once all files are ready the semantic
// passes (resolve → type-check → HIR → MIR → ownership) run sequentially
// in topological (dependency-first) order.
type Pipeline struct {
	ctx  *context.CompilerContext
	seen sync.Map // map[string]struct{} — parse-phase dedup
	wg   sync.WaitGroup
}

func New(ctx *context.CompilerContext) *Pipeline {
	return &Pipeline{ctx: ctx}
}

// ParseEntryForIDE parses a single entry file and its transitive imports, then
// runs just the semantic front-end (resolve + type-check).
//
// This is intended for editor tooling (LSP) where type information is needed
// but backend passes (HIR lowering/specialization, MIR, comptime, ownership)
// are too expensive and can cause large transient memory spikes.
func (p *Pipeline) ParseEntryForIDE(entryFile string) (*context.Module, error) {
	resolved, err := p.ctx.ResolveLocalModule(entryFile)
	if err != nil {
		return nil, err
	}
	p.scheduleParseFile(resolved, nil)
	p.wg.Wait()
	if mod, ok := p.ctx.GetModule(resolved.Key); ok && mod != nil {
		mod.IsEntry = true
		if synthesizeTestHarness(mod.AST, p.ctx.Config.TestMode, p.ctx.Config.TestName) {
			collector.CollectModule(p.ctx, mod)
		}
	}
	if err := p.runIDEPasses(); err != nil {
		return nil, err
	}
	p.runIDEFinalPasses()
	mod, _ := p.ctx.GetModule(resolved.Key)
	return mod, nil
}

// ParseWorkspaceForIDE parses all source files in the workspace root then runs
// resolve + type-check across them in dependency order.
func (p *Pipeline) ParseWorkspaceForIDE() ([]*context.Module, error) {
	files, err := p.ctx.DiscoverModules()
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		resolved, err := p.ctx.ResolveLocalModule(file)
		if err != nil {
			return nil, err
		}
		p.scheduleParseFile(resolved, nil)
	}
	p.wg.Wait()
	if err := p.runIDEPasses(); err != nil {
		return nil, err
	}
	p.runIDEFinalPasses()
	return p.ctx.Modules(), nil
}

// ParseEntry parses a single entry file and all its transitive imports.
func (p *Pipeline) ParseEntry(entryFile string) (*context.Module, error) {
	resolved, err := p.ctx.ResolveLocalModule(entryFile)
	if err != nil {
		return nil, err
	}
	p.scheduleParseFile(resolved, nil)
	p.wg.Wait()
	if mod, ok := p.ctx.GetModule(resolved.Key); ok && mod != nil {
		mod.IsEntry = true
		if synthesizeTestHarness(mod.AST, p.ctx.Config.TestMode, p.ctx.Config.TestName) {
			collector.CollectModule(p.ctx, mod)
		}
	}
	if err := p.runAllSemanticPasses(); err != nil {
		return nil, err
	}
	p.finalizeFinalPasses()
	mod, _ := p.ctx.GetModule(resolved.Key)
	return mod, nil
}

// ParseWorkspace parses all source files in the workspace root.
func (p *Pipeline) ParseWorkspace() ([]*context.Module, error) {
	files, err := p.ctx.DiscoverModules()
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		resolved, err := p.ctx.ResolveLocalModule(file)
		if err != nil {
			return nil, err
		}
		p.scheduleParseFile(resolved, nil)
	}
	p.wg.Wait()
	if err := p.runAllSemanticPasses(); err != nil {
		return nil, err
	}
	p.finalizeFinalPasses()
	return p.ctx.Modules(), nil
}

// scheduleParseFile enqueues a module for parallel lex+parse if not already
// scheduled.  Safe to call from multiple goroutines simultaneously.
func (p *Pipeline) scheduleParseFile(resolved context.ResolvedImport, loc *source.Location) {
	if _, loaded := p.seen.LoadOrStore(resolved.Key, struct{}{}); loaded {
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.parseFile(resolved, loc)
	}()
}

// parseFile lexes, parses, and symbol-collects one module, then schedules its
// imports as additional goroutines.  Runs concurrently for every reachable module.
func (p *Pipeline) parseFile(resolved context.ResolvedImport, loc *source.Location) {
	mod := p.ctx.UpsertModule(resolved)
	content, err := os.ReadFile(mod.FilePath)
	if err != nil {
		p.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot read module %s", mod.ImportPath)).
				WithCode(diagnostics.ErrModuleNotFound).
				WithPrimaryLabel(loc, err.Error()),
		)
		return
	}

	changed := p.ctx.StoreModuleContent(mod, string(content))
	p.ctx.Diagnostics.AddSourceContent(mod.FilePath, mod.Content)

	if mod.Phase >= phase.PhaseLayoutComputed && !changed {
		// Already fully compiled and unchanged — re-schedule known deps so they
		// also get the up-to-date check and are added to seen.
		for _, depKey := range p.ctx.DependencyList(mod.Key) {
			dep, ok := p.ctx.GetModule(depKey)
			if !ok {
				continue
			}
			p.scheduleParseFile(moduleRef(dep), nil)
		}
		return
	}

	mod.Phase = phase.PhaseLoaded
	stream := lexer.New(mod.FilePath, mod.Content, p.ctx.Diagnostics)
	mod.Tokens = stream.Tokenize()
	mod.Phase = phase.PhaseTokenized
	mod.AST = parser.Parse(mod.FilePath, mod.Tokens, p.ctx.Diagnostics)
	attrfilter.FilterModule(p.ctx, mod)
	mod.Phase = phase.PhaseParsed
	collector.CollectModule(p.ctx, mod)

	p.ctx.ResetDependencies(mod.Key)
	for _, imp := range mod.AST.Imports {
		dep, err := p.ctx.ResolveImport(mod, ast.ExprText(imp.Path))
		if err != nil {
			impLoc := imp.Location
			p.ctx.Diagnostics.Add(
				diagnostics.NewError("invalid import path").
					WithCode(diagnostics.ErrInvalidImportPath).
					WithPrimaryLabel(&impLoc, err.Error()),
			)
			continue
		}
		p.ctx.AddDependency(mod.Key, dep.Key)
		p.scheduleParseFile(dep, &imp.Location)
	}
}

// runAllSemanticPasses runs resolver → type-checker → HIR → MIR → ownership
// for every parsed module in topological (dependency-first) order.
func (p *Pipeline) runAllSemanticPasses() error {
	mods := p.ctx.Modules()
	sorted, cycles := topoSort(mods, p.ctx.DependencyList)
	for _, cycle := range cycles {
		p.reportCycle(cycle)
	}
	for _, mod := range sorted {
		if mod == nil || mod.Phase < phase.PhaseParsed {
			continue
		}
		p.runSemanticFrontPasses(mod)
	}
	p.runHIRLoweringAndSpecialization(sorted)
	for _, mod := range sorted {
		if mod == nil || mod.Phase < phase.PhaseHIRLowered {
			continue
		}
		p.runSemanticBackPasses(mod)
	}
	return nil
}

// runIDEPasses runs resolver → type-checker for every parsed module in
// topological (dependency-first) order.
func (p *Pipeline) runIDEPasses() error {
	mods := p.ctx.Modules()
	sorted, cycles := topoSort(mods, p.ctx.DependencyList)
	for _, cycle := range cycles {
		p.reportCycle(cycle)
	}
	for _, mod := range sorted {
		if mod == nil || mod.Phase < phase.PhaseParsed {
			continue
		}
		resolver.ResolveModule(p.ctx, mod)
		typechecker.CheckModule(p.ctx, mod)
	}
	return nil
}

func (p *Pipeline) runIDEFinalPasses() {
	if p == nil || p.ctx == nil || p.ctx.Diagnostics == nil {
		return
	}
	if p.ctx.Diagnostics.HasErrors() {
		return
	}
	usage.AnalyzeModules(p.ctx, p.ctx.Modules())
}

func (p *Pipeline) runSemanticFrontPasses(mod *context.Module) {
	resolver.ResolveModule(p.ctx, mod)
	typechecker.CheckModule(p.ctx, mod)
	mod.HIR = hir.Generate(mod.Key, mod.ImportPath, mod.FilePath, mod.AST, mod.Types, mod.Bindings, p.lookupMethodPath(mod.ImportPath))
	mod.Phase = phase.PhaseHIRGenerated
}

func (p *Pipeline) runHIRLoweringAndSpecialization(sorted []*context.Module) {
	if p == nil || p.ctx == nil {
		return
	}
	lowered := make([]*hir.Module, 0, len(sorted))
	typeInfos := make(map[string]*typeinfo.ModuleInfo, len(sorted))
	bindingInfos := make(map[string]*binding.ModuleInfo, len(sorted))
	for _, mod := range sorted {
		if mod == nil || mod.Phase < phase.PhaseHIRGenerated || mod.HIR == nil {
			continue
		}
		base := hir.Lower(mod.HIR)
		lowered = append(lowered, base)
		typeInfos[mod.Key] = mod.Types
		bindingInfos[mod.Key] = mod.Bindings
	}
	specialized := hir.SpecializeModules(lowered, typeInfos, bindingInfos)
	for _, mod := range sorted {
		if mod == nil || mod.Phase < phase.PhaseHIRGenerated {
			continue
		}
		if spec, ok := specialized[mod.Key]; ok && spec != nil {
			mod.LoweredHIR = spec
		} else if mod.HIR != nil {
			mod.LoweredHIR = hir.Specialize(hir.Lower(mod.HIR), mod.Types, mod.Bindings)
		}
		if mod.LoweredHIR != nil {
			mod.Phase = phase.PhaseHIRLowered
		}
	}
}

func (p *Pipeline) runSemanticBackPasses(mod *context.Module) {
	cfganalysis.AnalyzeModule(p.ctx, mod)
	mod.MIR = mir.LowerModule(mod.CFG, mod.LoweredHIR, mod.Bindings, p.buildGlobalConstMap(), p.lookupLoweredMethodPath(mod.ImportPath))
	mir.ValidateModule(p.ctx.Diagnostics, mod.MIR)
	mir.EvaluateComptime(p.ctx.Diagnostics, mod.MIR, p.lookupMIRFunction)
	mod.Phase = phase.PhaseMIRGenerated
	ownership.AnalyzeModule(p.ctx, mod)
	mod.Phase = phase.PhaseOwnershipAnalyzed
	mir.SimplifyModule(p.ctx.Diagnostics, mod.MIR)
	mir.ValidateModule(p.ctx.Diagnostics, mod.MIR)
	mod.Phase = phase.PhaseConstEvaluated
}

func (p *Pipeline) lookupMIRFunction(current *mir.Module, callee *mir.NameValue) (*mir.Module, *mir.Function, bool) {
	if p == nil || p.ctx == nil || callee == nil {
		return nil, nil, false
	}
	leaf := ""
	if len(callee.Path) > 0 {
		leaf = callee.Path[len(callee.Path)-1]
	}
	modulePath := ""
	if len(callee.Path) > 1 {
		modulePath = strings.Join(callee.Path[:len(callee.Path)-1], "/")
	}
	candidates := make([]*mir.Module, 0, 8)
	if modulePath != "" {
		for _, mod := range p.ctx.Modules() {
			if mod == nil || mod.MIR == nil || mod.ImportPath != modulePath {
				continue
			}
			candidates = append(candidates, mod.MIR)
		}
	}
	if len(candidates) == 0 && current != nil {
		candidates = append(candidates, current)
	}
	for _, mod := range p.ctx.Modules() {
		if mod == nil || mod.MIR == nil {
			continue
		}
		if current != nil && mod.MIR == current {
			continue
		}
		if modulePath != "" && mod.ImportPath == modulePath {
			continue
		}
		candidates = append(candidates, mod.MIR)
	}
	for _, mod := range candidates {
		if fn, ok := lookupFunctionInMIRModule(mod, callee, leaf); ok {
			return mod, fn, true
		}
	}
	return nil, nil, false
}

func lookupFunctionInMIRModule(mod *mir.Module, callee *mir.NameValue, leaf string) (*mir.Function, bool) {
	if mod == nil || callee == nil {
		return nil, false
	}
	for _, fn := range mod.Functions {
		if fn == nil {
			continue
		}
		if callee.LinkName != "" && fn.LinkName == callee.LinkName {
			return fn, true
		}
		if leaf != "" {
			if fn.LinkName == leaf || strings.HasSuffix(fn.LinkName, "__"+leaf) {
				return fn, true
			}
		}
		if fn.Name == leaf || fn.Name == strings.Join(callee.Path, "::") {
			return fn, true
		}
		if owner, method, ok := strings.Cut(fn.Name, "::"); ok {
			methodLeaf := owner + "__" + method
			if methodLeaf == leaf || method == leaf {
				return fn, true
			}
		}
	}
	return nil, false
}

func (p *Pipeline) lookupMethodPath(currentImportPath string) hir.MethodLookup {
	return func(receiver typeinfo.Type, methodName string) ([]string, bool) {
		if p == nil || p.ctx == nil || methodName == "" {
			return nil, false
		}
		named, ok := pipelineBaseNamed(receiver)
		if !ok || named == nil {
			return nil, false
		}
		owner, ok := p.ctx.GetModule(named.ModuleKey)
		if !ok || owner == nil || owner.MethodSets == nil {
			return nil, false
		}
		for _, key := range pipelineMethodCandidateKeys(named.Name, methodName) {
			methods := owner.MethodSets[key]
			if methods == nil {
				continue
			}
			sym := methods[methodName]
			if sym == nil {
				continue
			}
			leaf := pipelineMethodLinkLeaf(sym)
			return pipelineMethodPath(owner.ImportPath, currentImportPath, leaf), true
		}
		return nil, false
	}
}

func (p *Pipeline) lookupLoweredMethodPath(currentImportPath string) hir.MethodLookup {
	return func(receiver typeinfo.Type, methodName string) ([]string, bool) {
		if p == nil || p.ctx == nil || methodName == "" {
			return nil, false
		}
		named, ok := pipelineBaseNamed(receiver)
		if !ok || named == nil {
			return nil, false
		}
		owner, ok := p.ctx.GetModule(named.ModuleKey)
		if !ok || owner == nil || owner.LoweredHIR == nil {
			return nil, false
		}
		var method *hir.Func
		for _, fn := range owner.LoweredHIR.Functions {
			if fn == nil || fn.OwnerType != named.Name {
				continue
			}
			if fn.Name == methodName {
				method = fn
				break
			}
			if method == nil && strings.HasPrefix(fn.Name, methodName+"$") {
				method = fn
			}
		}
		if method == nil {
			return nil, false
		}
		leaf := method.Name
		if method.OwnerType != "" {
			leaf = method.OwnerType + "__" + method.Name
		}
		return pipelineMethodPath(owner.ImportPath, currentImportPath, leaf), true
	}
}

func pipelineMethodPath(ownerImportPath, currentImportPath, leaf string) []string {
	if ownerImportPath == "" || ownerImportPath == currentImportPath {
		return []string{leaf}
	}
	parts := strings.Split(ownerImportPath, "/")
	return append(parts, leaf)
}

func pipelineMethodLinkLeaf(sym *symbols.Symbol) string {
	if sym == nil || sym.Receiver.TypeName == "" {
		if sym == nil {
			return ""
		}
		return sym.Name
	}
	base := sym.Receiver.TypeName
	if base == "" {
		return sym.Name
	}
	return base + "__" + sym.Name
}

func pipelineBaseNamed(typ typeinfo.Type) (*typeinfo.NamedType, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t, true
	case *typeinfo.RefType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	default:
		return nil, false
	}
}

func pipelineMethodCandidateKeys(baseName, methodName string) []typeinfo.ReceiverKey {
	if baseName == "" || methodName == "" {
		return nil
	}
	if methodName == "~"+baseName {
		return []typeinfo.ReceiverKey{{Kind: typeinfo.ReceiverPtr, TypeName: baseName}}
	}
	if methodName == baseName {
		return []typeinfo.ReceiverKey{{Kind: typeinfo.ReceiverPtr, TypeName: baseName}}
	}
	return []typeinfo.ReceiverKey{
		{TypeName: baseName},
		{Kind: typeinfo.ReceiverRef, TypeName: baseName},
		{Kind: typeinfo.ReceiverRefMut, TypeName: baseName},
		{Kind: typeinfo.ReceiverPtr, TypeName: baseName},
	}
}

func (p *Pipeline) finalizeFinalPasses() {
	if p == nil || p.ctx == nil || p.ctx.Diagnostics == nil {
		return
	}
	if p.ctx.Diagnostics.HasErrors() {
		return
	}
	mods := p.ctx.Modules()
	usage.AnalyzeModules(p.ctx, mods)
	layoutanalysis.AnalyzeModules(p.ctx, mods)
}

func (p *Pipeline) buildGlobalConstMap() map[ast.Node]hir.Expr {
	if p == nil || p.ctx == nil {
		return nil
	}
	out := make(map[ast.Node]hir.Expr)
	for _, mod := range p.ctx.Modules() {
		if mod == nil || mod.LoweredHIR == nil {
			continue
		}
		for _, global := range mod.LoweredHIR.Globals {
			if global == nil || !global.Constant || global.Source == nil || global.Value == nil {
				continue
			}
			out[global.Source] = global.Value
		}
	}
	return out
}

func (p *Pipeline) reportCycle(cycle []string) {
	parts := make([]string, 0, len(cycle))
	for _, key := range cycle {
		if mod, ok := p.ctx.GetModule(key); ok && mod != nil {
			parts = append(parts, mod.ImportPath)
			continue
		}
		parts = append(parts, key)
	}
	message := "cyclic import: " + strings.Join(parts, " -> ")
	p.ctx.Diagnostics.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrCyclicImport),
	)
}

// topoSort returns modules in dependency-first order using DFS post-order.
// Any detected import cycles are returned separately as slices of module keys.
func topoSort(mods []*context.Module, deps func(key string) []string) ([]*context.Module, [][]string) {
	byKey := make(map[string]*context.Module, len(mods))
	for _, mod := range mods {
		if mod != nil {
			byKey[mod.Key] = mod
		}
	}

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(mods))
	result := make([]*context.Module, 0, len(mods))
	var cycles [][]string
	cycleKeys := make(map[string]struct{})

	var visit func(key string, path []string)
	visit = func(key string, path []string) {
		switch state[key] {
		case done:
			return
		case visiting:
			if _, already := cycleKeys[key]; already {
				return
			}
			for i, k := range path {
				if k == key {
					cycle := append(append([]string{}, path[i:]...), key)
					cycles = append(cycles, cycle)
					for _, ck := range cycle {
						cycleKeys[ck] = struct{}{}
					}
					return
				}
			}
			return
		}
		state[key] = visiting
		for _, dep := range deps(key) {
			if _, ok := byKey[dep]; ok {
				visit(dep, append(path, key))
			}
		}
		state[key] = done
		if mod, ok := byKey[key]; ok {
			result = append(result, mod)
		}
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if state[key] == unvisited {
			visit(key, nil)
		}
	}

	return result, cycles
}

func moduleRef(mod *context.Module) context.ResolvedImport {
	return context.ResolvedImport{
		Key:             mod.Key,
		ImportPath:      mod.ImportPath,
		FilePath:        mod.FilePath,
		Origin:          mod.Origin,
		DependencyAlias: mod.Dependency,
	}
}
