package main

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/diagnostics"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
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
    set(&mut self.routes, key, value)
}

fn main() -> void {
    let mut holder: Holder = .{ .routes = map[str]i32{} }
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
	if result.Entry != nil && result.Entry.MIR != nil {
		for _, fn := range result.Entry.MIR.Functions {
			if fn != nil {
				fmt.Println("fn:", fn.Name)
				for _, l := range fn.Locals {
					if l != nil {
						fmt.Printf("  local id=%d name=%s type=%T\n", l.ID, l.Name, l.Type)
					}
				}
			}
		}
		fmt.Println(mir.FormatModule(result.Entry.MIR))
	}
}
