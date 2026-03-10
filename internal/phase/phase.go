package phase

type ModulePhase int

const (
	PhaseNotLoaded ModulePhase = iota
	PhaseLoaded
	PhaseTokenized
	PhaseParsed
	PhaseCollected
	PhaseResolved
	PhaseTypeChecked
	PhaseHIRGenerated
	PhaseHIRLowered
	PhaseCFGAnalyzed
	PhaseMIRGenerated
	PhaseOwnershipAnalyzed
	PhaseConstEvaluated
	PhaseLLVMGenerated
	PhaseCodeGenerated
)

func (p ModulePhase) String() string {
	switch p {
	case PhaseNotLoaded:
		return "not_loaded"
	case PhaseLoaded:
		return "loaded"
	case PhaseTokenized:
		return "tokenized"
	case PhaseParsed:
		return "parsed"
	case PhaseCollected:
		return "collected"
	case PhaseResolved:
		return "resolved"
	case PhaseTypeChecked:
		return "type_checked"
	case PhaseHIRGenerated:
		return "hir_generated"
	case PhaseHIRLowered:
		return "hir_lowered"
	case PhaseCFGAnalyzed:
		return "cfg_analyzed"
	case PhaseMIRGenerated:
		return "mir_generated"
	case PhaseOwnershipAnalyzed:
		return "ownership_analyzed"
	case PhaseConstEvaluated:
		return "const_evaluated"
	case PhaseLLVMGenerated:
		return "llvm_generated"
	case PhaseCodeGenerated:
		return "code_generated"
	default:
		return "unknown"
	}
}
