package main

import (
    "fmt"
    "os"
    "path/filepath"

    "compiler/internal/compiler"
    "compiler/internal/diagnostics"
)

func main() {
    root, err := os.MkdirTemp("", "mirdiag-")
    if err != nil {
        panic(err)
    }
    defer os.RemoveAll(root)

    src := `type Holder struct {
    routes: map[str]i32
}

fn Holder::Add(&mut self, key: str, value: i32) -> void {
    _ = set(&mut self.routes, key, value)
}

fn main() -> void {
    let mut holder: Holder = .{ .routes = {} }
    holder.Add("x", 1)
}`
    path := filepath.Join(root, "main.fer")
    if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
        panic(err)
    }

    result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(path)
    for _, d := range result.Diagnostics.Diagnostics() {
        fmt.Printf("code=%s msg=%s\n", d.Code, d.Message)
    }
}
