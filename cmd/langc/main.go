package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/context"
	"compiler/internal/frontend/ast"
	midhir "compiler/internal/middleend/hir"
	midmir "compiler/internal/middleend/mir"
)

func main() {
	astFlag := flag.Bool("ast", false, "dump AST as JSON")
	astOut := flag.String("ast-out", "", "write AST JSON to file")
	hirFlag := flag.Bool("hir", false, "write HIR dump as .hir text")
	hirOut := flag.String("hir-out", "", "write HIR dump to file or directory")
	mirFlag := flag.Bool("mir", false, "write MIR dump as .mir text")
	mirOut := flag.String("mir-out", "", "write MIR dump to file or directory")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-ast] [-ast-out file] [-hir] [-hir-out path] [-mir] [-mir-out path] <source-file-or-directory>\n", os.Args[0])
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
	if *hirFlag || *hirOut != "" {
		if err := emitTextDump(result, *hirOut, ".hir", func(mod *context.Module) string {
			return midhir.FormatModule(mod.HIR)
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *mirFlag || *mirOut != "" {
		if err := emitTextDump(result, *mirOut, ".mir", func(mod *context.Module) string {
			return midmir.FormatModule(mod.MIR)
		}); err != nil {
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
	if *astFlag || *astOut != "" || *hirFlag || *hirOut != "" || *mirFlag || *mirOut != "" {
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

func emitTextDump(result compilerapi.Result, outPath, ext string, render func(*context.Module) string) error {
	mods := dumpModules(result)
	if len(mods) == 0 {
		return nil
	}
	if result.Entry != nil && outPath != "" && !pathLooksLikeDir(outPath) {
		content := render(result.Entry)
		if content == "" {
			return nil
		}
		return writeTextFile(ensureExt(outPath, ext), content)
	}
	for _, mod := range mods {
		content := render(mod)
		if content == "" {
			continue
		}
		target, err := dumpTargetPath(result, mod, outPath, ext)
		if err != nil {
			return err
		}
		if err := writeTextFile(target, content); err != nil {
			return err
		}
	}
	return nil
}

func dumpModules(result compilerapi.Result) []*context.Module {
	if result.Entry != nil {
		return []*context.Module{result.Entry}
	}
	mods := make([]*context.Module, 0, len(result.Modules))
	for _, mod := range result.Modules {
		if mod != nil {
			mods = append(mods, mod)
		}
	}
	return mods
}

func dumpTargetPath(result compilerapi.Result, mod *context.Module, outPath, ext string) (string, error) {
	if mod == nil {
		return "", fmt.Errorf("nil module")
	}
	if outPath == "" {
		return replaceExt(mod.FilePath, ext), nil
	}
	if result.Entry != nil {
		if pathLooksLikeDir(outPath) {
			return filepath.Join(outPath, filepath.Base(replaceExt(mod.FilePath, ext))), nil
		}
		return ensureExt(outPath, ext), nil
	}
	rel := filepath.FromSlash(mod.ImportPath) + ext
	return filepath.Join(outPath, rel), nil
}

func ensureExt(path, ext string) string {
	if filepath.Ext(path) == ext {
		return path
	}
	return replaceExt(path, ext)
}

func pathLooksLikeDir(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasSuffix(path, string(os.PathSeparator)) || strings.HasSuffix(path, "/") {
		return true
	}
	if info, err := os.Stat(path); err == nil {
		return info.IsDir()
	}
	return filepath.Ext(path) == ""
}

func replaceExt(path, ext string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ext
}

func writeTextFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
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
