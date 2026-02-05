package pipeline

import (
	"fmt"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	hirgen "compiler/internal/hir/gen"
	"compiler/internal/phase"
)

// runHIRGenerationPhase lowers typed AST to HIR for each module.
func (p *Pipeline) runHIRGenerationPhase() error {
	for _, importPath := range p.ctx.GetModuleNames() {
		module, exists := p.ctx.GetModule(importPath)
		if !exists {
			continue
		}

		if p.ctx.GetModulePhase(importPath) < phase.PhaseTypeChecked {
			continue
		}

		if module.Type == context_v2.ModuleBuiltin && module.AST != nil && len(module.AST.Nodes) == 0 {
			hir.StoreModule(module, &hir.Module{ImportPath: importPath})
			if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseHIRGenerated) {
				p.ctx.SetModulePhase(importPath, phase.PhaseHIRGenerated)
			}
			if p.ctx.Config.Debug {
				colors.PURPLE.Printf("  ✓ %s (native)\n", importPath)
			}
			continue
		}

		gen := hirgen.New(p.ctx, module)
		hirModule := gen.GenerateModule()
		if hirModule != nil {
			hir.StoreModule(module, hirModule)
		}

		if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseHIRGenerated) {
			p.ctx.ReportError(fmt.Sprintf("cannot advance module %s to PhaseHIRGenerated", importPath), nil)
		}

		if p.ctx.Config.Debug {
			colors.PURPLE.Printf("  ✓ %s\n", importPath)
		}
	}
	return nil
}
