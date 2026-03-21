package main

import (
	"path/filepath"
	"testing"

	"compiler/internal/core/context"
	compiler "compiler/internal/driver"
)

func TestAllModulesForBuildOrdersAndDedupes(t *testing.T) {
	m1 := &context.Module{Key: "a", ImportPath: "a"}
	m2 := &context.Module{Key: "b", ImportPath: "b"}
	entry := &context.Module{Key: "main", ImportPath: "main"}

	result := compiler.Result{
		Modules: []*context.Module{m1, entry, m2, m1},
		Entry:   entry,
	}
	got := allModulesForBuild(result)
	if len(got) != 3 {
		t.Fatalf("unexpected module count: %d", len(got))
	}
	if got[0].Key != "a" || got[1].Key != "b" || got[2].Key != "main" {
		t.Fatalf("unexpected module order: %s, %s, %s", got[0].Key, got[1].Key, got[2].Key)
	}
}

func TestModuleArtifactPath(t *testing.T) {
	mod := &context.Module{ImportPath: "std/io"}
	path, err := moduleArtifactPath(mod, "/tmp/out", ".mir")
	if err != nil {
		t.Fatalf("moduleArtifactPath error: %v", err)
	}
	want := filepath.Join("/tmp/out", filepath.FromSlash("std/io")+".mir")
	if path != want {
		t.Fatalf("moduleArtifactPath = %q, want %q", path, want)
	}
	if _, err := moduleArtifactPath(nil, "/tmp/out", ".mir"); err == nil {
		t.Fatalf("expected nil module error")
	}
	if _, err := moduleArtifactPath(mod, "", ".mir"); err == nil {
		t.Fatalf("expected empty output dir error")
	}
}
