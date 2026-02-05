package pipeline

import (
	"compiler/colors"
	"compiler/internal/codegen"
	"compiler/internal/context_v2"
	"compiler/internal/phase"
	ustrings "compiler/internal/utils/strings"
)

func (p *Pipeline) collectModulesForCodegen() []string {
	modulesToGenerate := []string{p.ctx.EntryModule}
	for _, importPath := range p.ctx.GetModuleNames() {
		if importPath == p.ctx.EntryModule {
			continue
		}
		module, exists := p.ctx.GetModule(importPath)
		if !exists {
			continue
		}

		modulePhase := p.ctx.GetModulePhase(importPath)
		include := false
		switch module.Type {
		case context_v2.ModuleLocal:
			include = modulePhase >= phase.PhaseMIRGenerated
		case context_v2.ModuleBuiltin:
			include = modulePhase >= phase.PhaseMIRGenerated
		}

		if include {
			modulesToGenerate = append(modulesToGenerate, importPath)
			if p.ctx.Config.Debug {
				colors.CYAN.Printf("  Including module: %s (phase: %s, type: %s)\n", importPath, modulePhase, module.Type)
			}
		} else if p.ctx.Config.Debug {
			colors.YELLOW.Printf("  Skipping module: %s (phase: %s, type: %s)\n", importPath, modulePhase, module.Type)
		}
	}
	return modulesToGenerate
}

func splitEntryModule(mods []string, entry string) (string, []string) {
	var entryModule string
	var importedModules []string
	for _, m := range mods {
		if m == entry {
			entryModule = m
		} else {
			importedModules = append(importedModules, m)
		}
	}
	return entryModule, importedModules
}

func (p *Pipeline) sanitizeModuleName(importPath string) string {
	return ustrings.ToIdentifier(importPath)
}

func (p *Pipeline) buildExecutableMultiple(asmFiles []string, execPath string) error {
	opts := codegen.DefaultBuildOptions()
	if p.ctx.Config.RuntimePath != "" {
		opts.RuntimePath = p.ctx.Config.RuntimePath
	}
	opts.OutputPath = execPath
	opts.Debug = p.ctx.Config.Debug

	return codegen.BuildExecutable(p.ctx, asmFiles, opts)
}
