package pipeline

import (
	"fmt"
	"os"
	"strings"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/hirgen"
	"compiler/internal/hirlower"
	"compiler/internal/phase"
	"compiler/internal/semantics/collector"
	"compiler/internal/semantics/ownership"
	"compiler/internal/semantics/resolver"
	"compiler/internal/semantics/typechecker"
	"compiler/internal/source"
)

type Pipeline struct {
	ctx *context.CompilerContext
}

func New(ctx *context.CompilerContext) *Pipeline {
	return &Pipeline{ctx: ctx}
}

func (p *Pipeline) ParseEntry(entryFile string) (*context.Module, error) {
	resolved, err := p.ctx.ResolveLocalModule(entryFile)
	if err != nil {
		return nil, err
	}
	if err := p.parseModule(resolved, nil); err != nil {
		return nil, err
	}
	mod, _ := p.ctx.GetModule(resolved.Key)
	return mod, nil
}

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
		if err := p.parseModule(resolved, nil); err != nil {
			return nil, err
		}
	}
	return p.ctx.Modules(), nil
}

func (p *Pipeline) parseModule(resolved context.ResolvedImport, stack []string) error {
	for idx, item := range stack {
		if item == resolved.Key {
			cycle := append(append([]string{}, stack[idx:]...), resolved.Key)
			p.reportCycle(cycle)
			return nil
		}
	}

	mod := p.ctx.UpsertModule(resolved)
	content, err := os.ReadFile(mod.FilePath)
	if err != nil {
		loc := importLocation(mod, stack)
		p.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot read module %s", mod.ImportPath)).
				WithCode(diagnostics.ErrModuleNotFound).
				WithPrimaryLabel(loc, err.Error()),
		)
		return nil
	}

	changed := p.ctx.StoreModuleContent(mod, string(content))
	p.ctx.Diagnostics.AddSourceContent(mod.FilePath, mod.Content)
	if mod.Phase >= phase.PhaseHIRLowered && !changed {
		for _, depKey := range p.ctx.DependencyList(mod.Key) {
			dep, ok := p.ctx.GetModule(depKey)
			if !ok {
				continue
			}
			if err := p.parseModule(moduleRef(dep), append(stack, mod.Key)); err != nil {
				return err
			}
		}
		return nil
	}

	mod.Phase = phase.PhaseLoaded
	stream := lexer.New(mod.FilePath, mod.Content, p.ctx.Diagnostics)
	mod.Tokens = stream.Tokenize()
	mod.Phase = phase.PhaseTokenized
	mod.AST = parser.Parse(mod.FilePath, mod.Tokens, p.ctx.Diagnostics)
	mod.Phase = phase.PhaseParsed
	collector.CollectModule(p.ctx, mod)

	p.ctx.ResetDependencies(mod.Key)
	for _, imp := range mod.AST.Imports {
		dep, err := p.ctx.ResolveImport(mod, imp.Path)
		if err != nil {
			loc := imp.Location
			p.ctx.Diagnostics.Add(
				diagnostics.NewError("invalid import path").
					WithCode(diagnostics.ErrInvalidImportPath).
					WithPrimaryLabel(&loc, err.Error()),
			)
			continue
		}
		p.ctx.AddDependency(mod.Key, dep.Key)
		if err := p.parseModule(dep, append(stack, mod.Key)); err != nil {
			return err
		}
	}
	resolver.ResolveModule(p.ctx, mod)
	typechecker.CheckModule(p.ctx, mod)
	hirgen.Generate(mod)
	ownership.AnalyzeModule(p.ctx, mod)
	hirlower.Lower(mod)
	return nil
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

func importLocation(mod *context.Module, stack []string) *source.Location {
	if mod != nil && mod.FilePath != "" {
		loc := source.NewLocation(mod.FilePath, source.NewPosition(), source.NewPosition())
		return &loc
	}
	if len(stack) == 0 {
		return nil
	}
	label := stack[len(stack)-1]
	loc := source.NewLocation(label, source.NewPosition(), source.NewPosition())
	return &loc
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
