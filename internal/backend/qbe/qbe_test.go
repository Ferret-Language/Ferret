package qbe_test

import (
	"io/fs"
	"testing"

	"compiler/internal/backend/qbe"
)

func TestVendoredSourceFS(t *testing.T) {
	root, err := qbe.SourceFS()
	if err != nil {
		t.Fatalf("expected vendored source fs: %v", err)
	}
	for _, path := range []string{"README", "Makefile", "doc/il.txt", "main.c"} {
		if _, err := fs.ReadFile(root, path); err != nil {
			t.Fatalf("expected embedded %s: %v", path, err)
		}
	}
	if qbe.VendoredCommit == "" {
		t.Fatal("expected vendored commit")
	}
}
