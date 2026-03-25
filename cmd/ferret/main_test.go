package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"compiler/colors"
	"compiler/internal/backend"
	"compiler/internal/core/context"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
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

func TestCountModuleTests(t *testing.T) {
	mod := &context.Module{
		AST: &ast.Module{
			Decls: []ast.Decl{
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"main"}}},
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"__ferret_test_0"}}, IsTest: true, TestName: "smoke"},
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"__ferret_test_1"}}, IsTest: true, TestName: "more"},
			},
		},
	}
	if got := countModuleTests(mod); got != 2 {
		t.Fatalf("countModuleTests = %d, want 2", got)
	}
}

func TestDisplayTestNamePrefersDeclaredLabel(t *testing.T) {
	fn := &ast.FuncDecl{
		Name:     &ast.Ident{Path: []string{"__ferret_test_0"}},
		IsTest:   true,
		TestName: "smoke",
	}
	if got := displayTestName(fn); got != "smoke" {
		t.Fatalf("displayTestName = %q, want %q", got, "smoke")
	}
}

func TestParseTestFailureOutputStructured(t *testing.T) {
	output := "__FERRET_TEST_FAIL__\n__FERRET_TEST_MESSAGE__\nmath broke\n__FERRET_TEST_EXPECTED__\n4\n__FERRET_TEST_GOT__\n5\n"
	got := parseTestFailureOutput(output)
	if !got.Known {
		t.Fatalf("expected known structured failure")
	}
	if got.Message != "math broke" {
		t.Fatalf("message = %q, want %q", got.Message, "math broke")
	}
	if got.Expected != "4" {
		t.Fatalf("expected = %q, want %q", got.Expected, "4")
	}
	if got.Got != "5" {
		t.Fatalf("got = %q, want %q", got.Got, "5")
	}
	if got.Raw != "" {
		t.Fatalf("raw = %q, want empty", got.Raw)
	}
}

func TestParseTestFailureOutputKeepsRawLines(t *testing.T) {
	output := "hello\n__FERRET_TEST_FAIL__\n__FERRET_TEST_MESSAGE__\nmath broke\nworld\n"
	got := parseTestFailureOutput(output)
	if got.Raw != "hello\nworld" {
		t.Fatalf("raw = %q, want %q", got.Raw, "hello\nworld")
	}
}

func TestParseCommandBackendDefaultsAndExplicitTargets(t *testing.T) {
	tests := []struct {
		in         string
		wantName   string
		wantTarget backend.Target
		wantErr    bool
	}{
		{in: "run", wantName: "run", wantTarget: backend.TargetLLVM},
		{in: "test", wantName: "test", wantTarget: backend.TargetLLVM},
		{in: "run:llvm", wantName: "run", wantTarget: backend.TargetLLVM},
		{in: "run:qbe", wantName: "run", wantTarget: backend.TargetQBE},
		{in: "test:llvm", wantName: "test", wantTarget: backend.TargetLLVM},
		{in: "test:qbe", wantName: "test", wantTarget: backend.TargetQBE},
		{in: "check", wantName: "check", wantTarget: ""},
		{in: "run:bad", wantErr: true},
		{in: "test:", wantName: "test", wantTarget: backend.TargetLLVM},
	}
	for _, tc := range tests {
		gotName, gotTarget, err := parseCommandBackend(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in, err)
		}
		if gotName != tc.wantName || gotTarget != tc.wantTarget {
			t.Fatalf("%q: got (%q, %q), want (%q, %q)", tc.in, gotName, gotTarget, tc.wantName, tc.wantTarget)
		}
	}
}

func TestPrintTestStatusUsesUppercaseColoredLabel(t *testing.T) {
	var buf bytes.Buffer
	printTestStatus(&buf, colors.GREEN, "OK", "smoke")
	got := buf.String()
	want := colors.GREEN.Sprintf("%-5s", "OK") + " smoke\n"
	if got != want {
		t.Fatalf("printTestStatus = %q, want %q", got, want)
	}
}
