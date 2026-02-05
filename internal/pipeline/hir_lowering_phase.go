package pipeline

import (
	"fmt"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	hirlower "compiler/internal/hir/lower"
	"compiler/internal/phase"
)

// runHIRLoweringPhase lowers HIR into a canonical form for downstream passes.
func (p *Pipeline) runHIRLoweringPhase() error {
	lowerer := hirlower.New(p.ctx)

	for _, importPath := range p.ctx.GetModuleNames() {
		module, exists := p.ctx.GetModule(importPath)
		if !exists {
			continue
		}

		if p.ctx.GetModulePhase(importPath) < phase.PhaseCFGAnalyzed {
			continue
		}

		if module.Type == context_v2.ModuleBuiltin && module.AST != nil && len(module.AST.Nodes) == 0 {
			hirMod := hir.ModuleFromModule(module)
			if hirMod == nil {
				hirMod = &hir.Module{ImportPath: importPath}
			}
			hir.StoreLoweredModule(module, hirMod)
			if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseHIRLowered) {
				p.ctx.SetModulePhase(importPath, phase.PhaseHIRLowered)
			}
			if p.ctx.Config.Debug {
				colors.PURPLE.Printf("  ✓ %s (native)\n", importPath)
			}
			continue
		}

		sourceHIR := hir.ModuleFromModule(module)
		if sourceHIR == nil {
			p.ctx.ReportError(fmt.Sprintf("HIR module missing for %s during HIR lowering", importPath), nil)
			continue
		}

		lowered := lowerer.LowerModule(sourceHIR)
		if lowered != nil {
			hir.StoreLoweredModule(module, lowered)
		}

		if !p.ctx.AdvanceModulePhase(importPath, phase.PhaseHIRLowered) {
			p.ctx.ReportError(fmt.Sprintf("cannot advance module %s to PhaseHIRLowered", importPath), nil)
		}

		if p.ctx.Config.Debug {
			colors.PURPLE.Printf("  ✓ %s\n", importPath)
		}
	}

	return nil
}
