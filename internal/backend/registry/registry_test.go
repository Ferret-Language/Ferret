package registry_test

import (
	"testing"

	"compiler/internal/backend"
	"compiler/internal/backend/registry"
)

func TestTargets(t *testing.T) {
	targets := registry.Targets()
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0] != backend.TargetLLVM || targets[1] != backend.TargetQBE {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestRegistry(t *testing.T) {
	qbe, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	if qbe.Target() != backend.TargetQBE {
		t.Fatalf("unexpected qbe target: %s", qbe.Target())
	}
	llvm, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	if llvm.Target() != backend.TargetLLVM {
		t.Fatalf("unexpected llvm target: %s", llvm.Target())
	}
}
