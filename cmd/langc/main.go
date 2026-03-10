package main

import (
	"fmt"
	"os"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/frontend/ast"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <source-file-or-directory>\n", os.Args[0])
		os.Exit(2)
	}

	result := compilerapi.ParsePath(os.Args[1])
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		os.Exit(1)
	}

	if result.Module != nil {
		for _, decl := range result.Module.Decls {
			fmt.Println(ast.DeclSummary(decl))
		}
		return
	}

	for _, mod := range result.Modules {
		fmt.Printf("module %s\n", mod.ImportPath)
		if mod.AST == nil {
			continue
		}
		for _, decl := range mod.AST.Decls {
			fmt.Printf("  %s\n", ast.DeclSummary(decl))
		}
	}
}
