package registry_test

import (
	"testing"

	"compiler/internal/backend"
	"compiler/internal/backend/registry"
)

func TestTargets(t *testing.T) {
	targets := registry.Targets()
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0] != backend.TargetLLVM {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestRegistry(t *testing.T) {
	llvm, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	if llvm.Target() != backend.TargetLLVM {
		t.Fatalf("unexpected llvm target: %s", llvm.Target())
	}
}
