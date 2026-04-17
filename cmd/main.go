package main

import (
	"fmt"
	"os"
	"strings"

	"compiler/colors"
	"compiler/internal/backend"
	"compiler/internal/frontend/ast"
)

func main() {
	exe, _ := os.Executable()
	if strings.Contains(exe, "go-build") {
		fmt.Println("run compiled program instead of 'go run'")
		os.Exit(1)
	}

	if parseAndRunCommand(os.Args[1:]) {
		return
	}

	opts := parseCompilerFlags()

	result := parsePathWithBackend(opts.inputPath, opts.backend, opts.debugBuild)
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		os.Exit(1)
	}

	if opts.keepGen {
		if err := emitKeepGenArtifacts(result, opts.backend, "_gen"); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		colors.GREEN.Fprintln(os.Stdout, "Generated artifacts in _gen")
	}

	if opts.outputPath != "" {
		if err := buildExecutable(result, opts.outputPath, backend.Target(opts.backend)); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		colors.GREEN.Fprintf(os.Stdout, "Built %s\n", opts.outputPath)
		return
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
