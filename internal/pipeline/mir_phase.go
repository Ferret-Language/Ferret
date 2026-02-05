package pipeline

import (
	"fmt"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/mir"
	mirgen "compiler/internal/mir/gen"
	"compiler/internal/phase"
)

// runMIRGenerationPhase lowers lowered HIR into MIR for each module.
func (p *Pipeline) runMIRGenerationPhase() error {
	for _, importPath := range p.ctx.GetModuleNames() {
		module, exists := p.ctx.GetModule(importPath)
		if !exists {
			continue
		}

		if p.ctx.GetModulePhase(importPath) < phase.PhaseHIRLowered {
			continue
		}

		if module.Type == context_v2.ModuleBuiltin && module.AST != nil && len(module.AST.Nodes) == 0 {
			mir.StoreModule(module, &mir.Module{ImportPath: importPath})
			if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseMIRGenerated) {
				p.ctx.SetModulePhase(importPath, phase.PhaseMIRGenerated)
			}
			if p.ctx.Config.Debug {
				colors.PURPLE.Printf("  ✓ %s (native)\n", importPath)
			}
			continue
		}

		loweredHIR := hir.LoweredModuleFromModule(module)
		if loweredHIR == nil {
			p.ctx.ReportError(fmt.Sprintf("HIR module missing for %s during MIR generation", importPath), nil)
			continue
		}

		gen := mirgen.New(p.ctx, module)
		mirModule := gen.GenerateModule(loweredHIR)
		if mirModule != nil {
			mir.StoreModule(module, mirModule)
			if p.ctx.Config.Debug && module.Type != context_v2.ModuleBuiltin && module.FilePath != "" {
				path := module.FilePath + ".mir"
				if err := mir.WriteModuleFile(mirModule, path); err != nil && p.ctx.Config.Debug {
					colors.YELLOW.Printf("  ⚠ Could not write MIR for %s: %v\n", importPath, err)
				}
			}
		}

		if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseMIRGenerated) {
			p.ctx.ReportError(fmt.Sprintf("cannot advance module %s to PhaseMIRGenerated", importPath), nil)
		}

		if p.ctx.Config.Debug {
			colors.PURPLE.Printf("  ✓ %s\n", importPath)
		}
	}
	return nil
}
