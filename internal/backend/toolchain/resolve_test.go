package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeNames(t *testing.T) {
	got := normalizeNames([]string{" clang ", "clang.exe", ""})
	if len(got) != 1 || got[0] != "clang" {
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

func TestBundledWindowsGCCRuntimeLibDirs(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "lib32", "gcc", "i686-w64-mingw32", "14.2.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}

	got := bundledWindowsGCCRuntimeLibDirs(filepath.Join(root, "lib32"))
	if len(got) != 1 {
		t.Fatalf("bundledWindowsGCCRuntimeLibDirs len = %d, want 1 (%#v)", len(got), got)
	}
	if got[0] != versionDir {
		t.Fatalf("bundledWindowsGCCRuntimeLibDirs[0] = %q, want %q", got[0], versionDir)
	}
}

func TestDarwinSDKRootUsesEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		return
	}
	const want = "/tmp/macos-sdk"
	prev := os.Getenv("SDKROOT")
	if err := os.Setenv("SDKROOT", want); err != nil {
		t.Fatalf("set SDKROOT: %v", err)
	}
	defer func() {
		if prev == "" {
			_ = os.Unsetenv("SDKROOT")
			return
		}
		_ = os.Setenv("SDKROOT", prev)
	}()

	if got := darwinSDKRoot(); got != want {
		t.Fatalf("darwinSDKRoot() = %q, want %q", got, want)
	}
}

func TestClangDriverArgsIncludeDarwinSDKRootFromEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		return
	}
	const sdkRoot = "/tmp/macos-sdk"
	prev := os.Getenv("SDKROOT")
	if err := os.Setenv("SDKROOT", sdkRoot); err != nil {
		t.Fatalf("set SDKROOT: %v", err)
	}
	defer func() {
		if prev == "" {
			_ = os.Unsetenv("SDKROOT")
			return
		}
		_ = os.Setenv("SDKROOT", prev)
	}()

	got := ClangDriverArgs("/tmp/ferret/toolchain/bin/clang", 64)
	for i := 0; i < len(got)-1; i++ {
		if got[i] == "-isysroot" && got[i+1] == sdkRoot {
			return
		}
	}
	t.Fatalf("ClangDriverArgs missing darwin -isysroot %q: %#v", sdkRoot, got)
}
