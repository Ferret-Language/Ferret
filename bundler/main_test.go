package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBinaryNameAndIsFile(t *testing.T) {
	got := binaryName("ferret")
	if runtime.GOOS == "windows" {
		if got != "ferret.exe" {
			t.Fatalf("binaryName mismatch: %q", got)
		}
	} else if got != "ferret" {
		t.Fatalf("binaryName mismatch: %q", got)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isFile(file) {
		t.Fatalf("expected file path to be detected")
	}
	if isFile(dir) {
		t.Fatalf("expected dir path to be false")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "nested", "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile error: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("copied content mismatch: %q", string(got))
	}
}

func TestSkipBundledSharedLibraryLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		return
	}
	if !skipBundledSharedLibrary("/lib/x86_64-linux-gnu/libc.so.6") {
		t.Fatalf("expected libc to be skipped")
	}
	if skipBundledSharedLibrary("/opt/custom/libsomething.so") {
		t.Fatalf("custom library should not be skipped")
	}
}
