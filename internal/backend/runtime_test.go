package backend

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/abi"
)

func TestRuntimeStaticLibName(t *testing.T) {
	if got := RuntimeStaticLibName(abi.Bits32); got != "ferret_runtime32.a" {
		t.Fatalf("RuntimeStaticLibName(32) = %q, want ferret_runtime32.a", got)
	}
	if got := RuntimeStaticLibName(abi.Bits64); got != "ferret_runtime.a" {
		t.Fatalf("RuntimeStaticLibName(64) = %q, want ferret_runtime.a", got)
	}
}

func TestRuntimeStaticLibUsesActiveABIArchive(t *testing.T) {
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	prevBits := abi.SizeBits()
	t.Cleanup(func() {
		if chdirErr := os.Chdir(prevDir); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
		if setErr := abi.SetSizeBits(prevBits); setErr != nil {
			t.Fatalf("restore abi bits: %v", setErr)
		}
	})

	root := t.TempDir()
	projectDir := filepath.Join(root, "project", "nested")
	libsDir := filepath.Join(root, "project", "libs")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.MkdirAll(libsDir, 0o755); err != nil {
		t.Fatalf("mkdir libs dir: %v", err)
	}
	for _, name := range []string{"ferret_runtime.a", "ferret_runtime32.a"} {
		if err := os.WriteFile(filepath.Join(libsDir, name), []byte("archive"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := abi.SetSizeBits(abi.Bits32); err != nil {
		t.Fatalf("set 32-bit abi: %v", err)
	}
	libPath, err := RuntimeStaticLib()
	if err != nil {
		t.Fatalf("RuntimeStaticLib() 32-bit: %v", err)
	}
	if want := filepath.Join(libsDir, "ferret_runtime32.a"); libPath != want {
		t.Fatalf("RuntimeStaticLib() 32-bit = %q, want %q", libPath, want)
	}

	if err := abi.SetSizeBits(abi.Bits64); err != nil {
		t.Fatalf("set 64-bit abi: %v", err)
	}
	libPath, err = RuntimeStaticLib()
	if err != nil {
		t.Fatalf("RuntimeStaticLib() 64-bit: %v", err)
	}
	if want := filepath.Join(libsDir, "ferret_runtime.a"); libPath != want {
		t.Fatalf("RuntimeStaticLib() 64-bit = %q, want %q", libPath, want)
	}
}
