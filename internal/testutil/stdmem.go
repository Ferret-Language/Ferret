package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteStdMemFixture(t testing.TB, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
#[extern("malloc")]
fn malloc(size: usize) -> ^void;

#[extern("free")]
fn free(ptr: ^void) -> void;

#[extern("ferret_global_slice_len")]
fn len(value: []u8) -> usize;
`)
	writeFixture(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
type Allocator interface {
    Alloc(&self, size: usize) -> ^void
    Free(&self, ptr: ^void) -> void
}

type CAllocator struct {}

fn CAllocator::Alloc(&self, size: usize) -> ^void {
    return malloc(size)
}

fn CAllocator::Free(&self, ptr: ^void) -> void {
    free(ptr)
}

fn System() -> CAllocator {
    return .{}
}

fn Alloc(a: Allocator, size: usize) -> ^void {
    return a.Alloc(size)
}

fn AllocAs<T>(a: Allocator, size: usize) -> ^T {
    unsafe {
        return Alloc(a, size) as ^T
    }
}

fn Free(a: Allocator, ptr: ^void) -> void {
    a.Free(ptr)
}

fn FreeAs<T>(a: Allocator, ptr: ^T) -> void {
    unsafe {
        Free(a, ptr as ^void)
    }
}
`)
}

func writeFixture(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
