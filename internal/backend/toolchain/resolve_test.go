package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeNames(t *testing.T) {
	got := normalizeNames([]string{" clang ", "clang.exe", "", "qbe"})
	if len(got) != 2 || got[0] != "clang" || got[1] != "qbe" {
		t.Fatalf("normalizeNames mismatch: %#v", got)
	}
}

func TestExeName(t *testing.T) {
	got := exeName("ferret")
	if runtime.GOOS == "windows" {
		if got != "ferret.exe" {
			t.Fatalf("exeName mismatch on windows: %q", got)
		}
		return
	}
	if got != "ferret" {
		t.Fatalf("exeName mismatch: %q", got)
	}
}

func TestUniqueDirs(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	dups := []string{a, filepath.Clean(a), b, ""}
	out := uniqueDirs(dups)
	if len(out) != 2 {
		t.Fatalf("uniqueDirs mismatch: %#v", out)
	}
	out = uniqueExistingDirs(append(dups, filepath.Join(a, "missing")))
	if len(out) != 2 {
		t.Fatalf("uniqueExistingDirs mismatch: %#v", out)
	}
}

func TestResolveBundledBinaryPrefersOnlyBundledPaths(t *testing.T) {
	root := t.TempDir()
	toolchainDir := filepath.Join(root, "build", "toolchain", "bin")
	if err := os.MkdirAll(toolchainDir, 0o755); err != nil {
		t.Fatalf("mkdir toolchain dir: %v", err)
	}

	name := exeName("clang")
	bundledPath := filepath.Join(toolchainDir, name)
	if err := os.WriteFile(bundledPath, []byte("x"), 0o755); err != nil {
		t.Fatalf("write bundled binary: %v", err)
	}

	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(prevDir); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()

	got, err := ResolveBundledBinary("clang")
	if err != nil {
		t.Fatalf("ResolveBundledBinary returned error: %v", err)
	}
	if got != bundledPath {
		t.Fatalf("ResolveBundledBinary = %q, want %q", got, bundledPath)
	}
}

func TestClangDriverArgsUseBundledLayout(t *testing.T) {
	got := ClangDriverArgs("/tmp/ferret/toolchain/bin/clang", 32)
	want := []string{
		"-fuse-ld=lld",
		"-B/tmp/ferret/toolchain/bin",
		"-B/tmp/ferret/toolchain/lib32",
		"-L/tmp/ferret/toolchain/lib32",
		"-isystem", "/tmp/ferret/toolchain/include",
	}
	if len(got) != len(want) {
		t.Fatalf("ClangDriverArgs len = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ClangDriverArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
