package toml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFileSupportsInlineTablesAndArrays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fer.ret")
	src := `
[package]
name = "app"

[dependencies]
json = { type = "remote", repo = "github.com/acme/json", version = "v1.2.0" }
flags = [true, 1, "x"]
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	deps := data.Sections["dependencies"]
	jsonTable, ok := deps["json"].(Table)
	if !ok {
		t.Fatalf("expected inline table, got %#v", deps["json"])
	}
	if jsonTable["repo"] != "github.com/acme/json" {
		t.Fatalf("unexpected repo: %#v", jsonTable["repo"])
	}
	flags, ok := deps["flags"].([]Value)
	if !ok || len(flags) != 3 {
		t.Fatalf("unexpected array: %#v", deps["flags"])
	}
}

func TestParseFileRejectsDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fer.ret")
	src := `
[package]
name = "app"
name = "other"
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ParseFile(path); err == nil {
		t.Fatal("expected duplicate key error")
	}
}
