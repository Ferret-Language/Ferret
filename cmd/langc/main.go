package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compiler/internal/backend"
	"compiler/internal/backend/llvm"
	"compiler/internal/backend/qbe"
	"compiler/internal/backend/registry"
	compilerapi "compiler/internal/compiler"
	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/layout"
	midhir "compiler/internal/middleend/hir"
	midmir "compiler/internal/middleend/mir"
	"compiler/internal/project"
)

func main() {
	astFlag := flag.Bool("ast", false, "dump AST as JSON")
	astOut := flag.String("ast-out", "", "write AST JSON to file")
	hirFlag := flag.Bool("hir", false, "write HIR dump as .hir text")
	hirOut := flag.String("hir-out", "", "write HIR dump to file or directory")
	mirFlag := flag.Bool("mir", false, "write MIR dump as .mir text")
	mirOut := flag.String("mir-out", "", "write MIR dump to file or directory")
	backendTarget := flag.String("backend", "", "lower to backend IR target (qbe|llvm)")
	backendOut := flag.String("backend-out", "", "write backend IR to file or directory")
	outputPath := flag.String("o", "", "compile and link to executable (see -build-backend)")
	buildBackend := flag.String("build-backend", "qbe", "backend to use for -o compilation (qbe|llvm)")
	debugBuild := flag.Bool("debug", false, "enable debug build mode (emits debug info and debug-friendly codegen)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-o output] [-ast] [-ast-out file] [-hir] [-hir-out path] [-mir] [-mir-out path] [-backend target] [-backend-out path] <source-file-or-directory>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	selectedBackend := *backendTarget
	if selectedBackend == "" {
		selectedBackend = *buildBackend
	}
	if selectedBackend == "" {
		selectedBackend = "qbe"
	}
	result := parsePathWithBackend(flag.Arg(0), selectedBackend, *debugBuild)
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
	if *outputPath != "" {
		if err := buildExecutable(result, *outputPath, backend.Target(*buildBackend)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *backendTarget != "" {
		if err := emitBackend(result, *backendTarget, *backendOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *astFlag || *astOut != "" || *hirFlag || *hirOut != "" || *mirFlag || *mirOut != "" {
		if *backendTarget != "" {
			return
		}
		return
	}
	if *backendTarget != "" {
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

func parsePathWithBackend(path, targetBackend string, buildDebug bool) compilerapi.Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compilerapi.Result{Diagnostics: diag}
	}
	info, err := os.Stat(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compilerapi.Result{Diagnostics: diag}
	}
	if info.IsDir() {
		ws, err := project.Load(absPath, ".ferr")
		if err != nil {
			diag.Add(diagnostics.NewError(err.Error()))
			return compilerapi.Result{Diagnostics: diag}
		}
		ws.Context.TargetBackend = targetBackend
		ws.Context.BuildDebug = buildDebug
		compiler := compilerapi.NewWithConfig(ws.Context, diag)
		return compiler.ParseWorkspace()
	}
	ws, err := project.Load(absPath, filepath.Ext(absPath))
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compilerapi.Result{Diagnostics: diag}
	}
	ws.Context.TargetBackend = targetBackend
	ws.Context.BuildDebug = buildDebug
	compiler := compilerapi.NewWithConfig(ws.Context, diag)
	return compiler.ParseEntry(absPath)
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

func emitBackend(result compilerapi.Result, targetText, outPath string) error {
	target := backend.Target(strings.ToLower(strings.TrimSpace(targetText)))
	lowerer, err := registry.New(target)
	if err != nil {
		return err
	}
	mods := dumpModules(result)
	for _, mod := range mods {
		if mod == nil || mod.MIR == nil || mod.Layout == nil {
			continue
		}
		artifact, err := lowerer.LowerModule(&backend.Unit{
			Module:  mod.MIR,
			Layout:  mod.Layout,
			Layouts: backendLayouts(result),
			Modules: backendModules(result),
		})
		if err != nil {
			return fmt.Errorf("backend %s lower %s: %w", target, mod.ImportPath, err)
		}
		targetPath, err := backendTargetPath(result, mod, outPath, artifact.FileExt)
		if err != nil {
			return err
		}
		if err := writeTextFile(targetPath, artifact.Text); err != nil {
			return err
		}
	}
	return nil
}

func backendTargetPath(result compilerapi.Result, mod *context.Module, outPath, ext string) (string, error) {
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

func backendLayouts(result compilerapi.Result) map[string]*layout.Module {
	layouts := make(map[string]*layout.Module)
	for _, mod := range result.Modules {
		if mod != nil && mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
	}
	if len(layouts) == 0 && result.Entry != nil && result.Entry.Layout != nil {
		layouts[result.Entry.Key] = result.Entry.Layout
	}
	return layouts
}

func backendModules(result compilerapi.Result) map[string]*midmir.Module {
	modules := make(map[string]*midmir.Module)
	for _, mod := range result.Modules {
		if mod != nil && mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
	}
	if len(modules) == 0 && result.Entry != nil && result.Entry.MIR != nil {
		modules[result.Entry.Key] = result.Entry.MIR
	}
	return modules
}

// buildExecutable lowers all modules to IR, concatenates them,
// appends an entry wrapper, then compiles and links into a native executable.
func buildExecutable(result compilerapi.Result, outputPath string, target backend.Target) error {
	// For workspace builds, Entry may be nil. Find the module that contains
	// a "main" function and use it as the entry point.
	if result.Entry == nil {
		for _, mod := range result.Modules {
			if mod == nil || mod.MIR == nil {
				continue
			}
			for _, fn := range mod.MIR.Functions {
				if fn != nil && fn.Name == "main" {
					result.Entry = mod
					break
				}
			}
			if result.Entry != nil {
				break
			}
		}
	}
	if result.Entry == nil {
		return fmt.Errorf("build: no entry module")
	}

	layouts := backendLayouts(result)

	absOut, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("build: output path: %w", err)
	}
	debugBuild := result.CompilerState != nil && result.CompilerState.Config.BuildDebug

	switch target {
	case backend.TargetLLVM:
		// Build units for all modules and lower them in one program-wide pass.
		// LowerProgram emits all type declarations before any function bodies,
		// producing a single self-contained LLVM IR file without the need for
		// any post-processing.
		var units []*backend.Unit
		for _, mod := range allModulesForBuild(result) {
			if mod == nil || mod.MIR == nil || mod.Layout == nil {
				continue
			}
			units = append(units, &backend.Unit{
				Module:  mod.MIR,
				Layout:  mod.Layout,
				Layouts: layouts,
				Modules: backendModules(result),
			})
		}
		ir, err := llvm.LowerProgram(units, debugBuild)
		if err != nil {
			return fmt.Errorf("build: %w", err)
		}
		wrapper, err := llvm.MainWrapper(result.Entry.MIR)
		if err != nil {
			return fmt.Errorf("build: %w", err)
		}
		return llvm.CompileIR(ir+wrapper, absOut, llvm.CompileOptions{Debug: debugBuild})
	default:
		lowerer, err := registry.New(target)
		if err != nil {
			return fmt.Errorf("build: %w", err)
		}
		var combined strings.Builder
		for _, mod := range allModulesForBuild(result) {
			if mod == nil || mod.MIR == nil || mod.Layout == nil {
				continue
			}
			artifact, err := lowerer.LowerModule(&backend.Unit{
				Module:  mod.MIR,
				Layout:  mod.Layout,
				Layouts: layouts,
				Modules: backendModules(result),
			})
			if err != nil {
				return fmt.Errorf("build: lower %s: %w", mod.ImportPath, err)
			}
			combined.WriteString(artifact.Text)
			combined.WriteByte('\n')
		}
		wrapper, err := qbe.MainWrapper(result.Entry.MIR)
		if err != nil {
			return fmt.Errorf("build: %w", err)
		}
		combined.WriteString(wrapper)
		return qbe.CompileIR(combined.String(), absOut)
	}
}

// allModulesForBuild returns all modules ordered so imports come before the
// entry module. Avoids duplicates.
func allModulesForBuild(result compilerapi.Result) []*context.Module {
	seen := make(map[string]struct{})
	all := make([]*context.Module, 0, len(result.Modules)+1)
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		if result.Entry != nil && mod.Key == result.Entry.Key {
			continue
		}
		if _, ok := seen[mod.Key]; ok {
			continue
		}
		seen[mod.Key] = struct{}{}
		all = append(all, mod)
	}
	if result.Entry != nil {
		all = append(all, result.Entry)
	}
	return all
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
