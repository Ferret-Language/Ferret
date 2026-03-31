package common

import (
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
)

func TestRuntimeTypeKeyQualifiesNamedTypesByModule(t *testing.T) {
	mainName := &typeinfo.NamedType{ModuleKey: "main", Name: "Name"}
	utilName := &typeinfo.NamedType{ModuleKey: "util/name", Name: "Name"}

	if got := RuntimeTypeKey(mainName); got != "main__Name" {
		t.Fatalf("expected main qualified key, got %q", got)
	}
	if got := RuntimeTypeKey(utilName); got != "util__name__Name" {
		t.Fatalf("expected imported qualified key, got %q", got)
	}
}
