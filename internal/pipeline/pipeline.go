package pipeline

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/phase"
	"compiler/internal/source"
)

type Pipeline struct {
	ctx *context.CompilerContext
}

func New(ctx *context.CompilerContext) *Pipeline {
	return &Pipeline{ctx: ctx}
}

func (p *Pipeline) ParseEntry(entryFile string) (*context.Module, error) {
	importPath, err := p.ctx.ImportPathForFile(entryFile)
	if err != nil {
		return nil, err
	}
	if err := p.parseModule(importPath, nil); err != nil {
		return nil, err
	}
	mod, _ := p.ctx.GetModule(importPath)
	return mod, nil
}

func (p *Pipeline) ParseWorkspace() ([]*context.Module, error) {
	files, err := p.ctx.DiscoverModules()
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		importPath, err := p.ctx.ImportPathForFile(file)
		if err != nil {
			return nil, err
		}
		if err := p.parseModule(importPath, nil); err != nil {
			return nil, err
		}
	}
	return p.sortedModules(files)
}

func (p *Pipeline) parseModule(importPath string, stack []string) error {
	for idx, item := range stack {
		if item == importPath {
			cycle := append(append([]string{}, stack[idx:]...), importPath)
			p.reportCycle(cycle)
			return nil
		}
	}

	mod := p.ctx.UpsertModule(importPath)
	content, err := os.ReadFile(mod.FilePath)
	if err != nil {
		loc := importLocation(mod, stack)
		p.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot read module %s", importPath)).
				WithCode(diagnostics.ErrModuleNotFound).
				WithPrimaryLabel(loc, err.Error()),
		)
		return nil
	}

	changed := p.ctx.StoreModuleContent(mod, string(content))
	p.ctx.Diagnostics.AddSourceContent(mod.FilePath, mod.Content)
	if mod.Phase >= phase.PhaseParsed && !changed {
		for _, dep := range p.ctx.DependencyList(importPath) {
			if err := p.parseModule(dep, append(stack, importPath)); err != nil {
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

	p.ctx.ResetDependencies(importPath)
	for _, imp := range mod.AST.Imports {
		resolved, err := p.ctx.ResolveImport(mod, imp.Path)
		if err != nil {
			loc := imp.Location
			p.ctx.Diagnostics.Add(
				diagnostics.NewError("invalid import path").
					WithCode(diagnostics.ErrInvalidImportPath).
					WithPrimaryLabel(&loc, err.Error()),
			)
			continue
		}
		p.ctx.AddDependency(importPath, resolved)
		if err := p.parseModule(resolved, append(stack, importPath)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) reportCycle(cycle []string) {
	message := "cyclic import: " + strings.Join(cycle, " -> ")
	p.ctx.Diagnostics.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrCyclicImport),
	)
}

func (p *Pipeline) sortedModules(files []string) ([]*context.Module, error) {
	seen := make(map[string]struct{}, len(files))
	imports := make([]string, 0, len(files))
	for _, file := range files {
		importPath, err := p.ctx.ImportPathForFile(file)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[importPath]; ok {
			continue
		}
		seen[importPath] = struct{}{}
		imports = append(imports, importPath)
	}
	sort.Strings(imports)

	mods := make([]*context.Module, 0, len(imports))
	for _, importPath := range imports {
		mod, ok := p.ctx.GetModule(importPath)
		if !ok {
			continue
		}
		mods = append(mods, mod)
	}
	return mods, nil
}

func importLocation(mod *context.Module, stack []string) *source.Location {
	if mod != nil && mod.FilePath != "" {
		loc := source.NewLocation(mod.FilePath, source.NewPosition(), source.NewPosition())
		return &loc
	}
	if len(stack) == 0 {
		return nil
	}
	loc := source.NewLocation(stack[len(stack)-1], source.NewPosition(), source.NewPosition())
	return &loc
}
