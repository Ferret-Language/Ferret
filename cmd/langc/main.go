package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/frontend/ast"
	midir "compiler/internal/middleend/ir"
)

func main() {
	astFlag := flag.Bool("ast", false, "dump AST as JSON")
	astOut := flag.String("ast-out", "", "write AST JSON to file")
	irFlag := flag.Bool("ir", false, "dump backend-independent IR as JSON")
	irOut := flag.String("ir-out", "", "write IR JSON to file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-ast] [-ast-out file] [-ir] [-ir-out file] <source-file-or-directory>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	result := compilerapi.ParsePath(flag.Arg(0))
	if *astFlag || *astOut != "" {
		if err := emitASTDump(result, *astOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *irFlag || *irOut != "" {
		if err := emitIRDump(result, *irOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		os.Exit(1)
	}
	if *astFlag || *astOut != "" || *irFlag || *irOut != "" {
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

func emitASTDump(result compilerapi.Result, outPath string) error {
	payload := debugPayload(result)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ast dump: %w", err)
	}
	if outPath == "" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(outPath, append(data, '\n'), 0o644)
}

func emitIRDump(result compilerapi.Result, outPath string) error {
	payload := debugIRPayload(result)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ir dump: %w", err)
	}
	if outPath == "" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(outPath, append(data, '\n'), 0o644)
}

func debugPayload(result compilerapi.Result) any {
	if result.Entry != nil {
		return map[string]any{
			"kind":        "entry",
			"import_path": result.Entry.ImportPath,
			"file_path":   result.Entry.FilePath,
			"origin":      string(result.Entry.Origin),
			"phase":       result.Entry.Phase.String(),
			"ast":         ast.DebugModule(result.Entry.AST),
		}
	}
	modules := make([]any, 0, len(result.Modules))
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		modules = append(modules, map[string]any{
			"import_path":  mod.ImportPath,
			"file_path":    mod.FilePath,
			"origin":       string(mod.Origin),
			"phase":        mod.Phase.String(),
			"dependencies": append([]string(nil), mod.Dependencies...),
			"ast":          ast.DebugModule(mod.AST),
		})
	}
	return map[string]any{
		"kind":    "workspace",
		"modules": modules,
	}
}

func debugIRPayload(result compilerapi.Result) any {
	if result.Entry != nil {
		return map[string]any{
			"kind":        "entry",
			"import_path": result.Entry.ImportPath,
			"file_path":   result.Entry.FilePath,
			"origin":      string(result.Entry.Origin),
			"phase":       result.Entry.Phase.String(),
			"ir":          midir.DebugModule(result.Entry.IR),
		}
	}
	modules := make([]any, 0, len(result.Modules))
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		modules = append(modules, map[string]any{
			"import_path":  mod.ImportPath,
			"file_path":    mod.FilePath,
			"origin":       string(mod.Origin),
			"phase":        mod.Phase.String(),
			"dependencies": append([]string(nil), mod.Dependencies...),
			"ir":           midir.DebugModule(mod.IR),
		})
	}
	return map[string]any{
		"kind":    "workspace",
		"modules": modules,
	}
}
