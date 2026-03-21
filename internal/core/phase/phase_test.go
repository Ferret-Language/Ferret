package phase

import "testing"

func TestModulePhaseString(t *testing.T) {
	if got := PhaseParsed.String(); got != "parsed" {
		t.Fatalf("PhaseParsed.String() = %q", got)
	}
	if got := ModulePhase(999).String(); got != "unknown" {
		t.Fatalf("unknown phase string = %q", got)
	}
}
