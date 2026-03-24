package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"compiler/cmd/ferret/cli"
	"compiler/colors"
	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/backend"
	"compiler/internal/backend/llvm"
	"compiler/internal/backend/qbe"
	"compiler/internal/backend/registry"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/manifest"
	"compiler/internal/core/project"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/hir"
	"compiler/internal/ir/mir"
	"compiler/internal/lsp"
)

func main() {
	if len(os.Args) > 1 {
		command := os.Args[1]
		commandArgs := os.Args[2:]
		switch command {
		case "lsp":
			colors.CYAN.Fprintln(os.Stderr, "starting Ferret LSP server...")
			if err := lsp.Run(os.Stdin, os.Stdout); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "init":
			if err := cli.InitCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "get":
			if err := cli.GetCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "update":
			if err := cli.UpdateCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "remove", "rm":
			if err := cli.RemoveCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "list", "ls":
			if err := cli.ListCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "clean":
			if err := cli.CleanupCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "run":
			if err := runCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "check", "lint":
			if err := checkCommand(commandArgs); err != nil {
				colors.RED.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	backendTarget := flag.String("backend", "llvm", "backend target (llvm|qbe)")
	outputPath := flag.String("o", "", "compile and link to executable")
	keepGen := flag.Bool("keep-gen", false, "keep generated AST/HIR/MIR/backend IR in _gen directory")
	flag.BoolVar(keepGen, "k", false, "alias for -keep-gen")
	debugBuild := flag.Bool("debug", false, "enable debug build mode (emits debug info and debug-friendly codegen)")
	showVersion := flag.Bool("version", false, "show compiler version")
	flag.BoolVar(showVersion, "v", false, "alias for -version")
	showHelp := flag.Bool("help", false, "show help")
	flag.BoolVar(showHelp, "h", false, "alias for -help")
	flag.Usage = func() {
		colors.BLUE.Fprintln(os.Stderr, "Ferret compiler v"+compiler.CompilerVersion)
		colors.CYAN.Fprintln(os.Stderr, "\nUsage:")
		colors.GREEN.Fprintf(os.Stderr, "  %s [options] <source-file-or-directory>\n", os.Args[0])
		colors.GREEN.Fprintf(os.Stderr, "  %s [command] [args]\n", os.Args[0])
		colors.CYAN.Fprintln(os.Stderr, "\nOptions:")
		flag.PrintDefaults()
		colors.CYAN.Fprintln(os.Stderr, "\nCommands:")
		fmt.Fprintln(os.Stderr, "  init [name]             create a new project with fer.ret")
		fmt.Fprintln(os.Stderr, "  get [pkg ...]           install dependencies from fer.ret or specific packages")
		fmt.Fprintln(os.Stderr, "  update [pkg ...]        update locked dependencies")
		fmt.Fprintln(os.Stderr, "  remove|rm <alias>       remove dependency alias from fer.ret and lockfile")
		fmt.Fprintln(os.Stderr, "  list|ls                 list direct and transitive dependencies")
		fmt.Fprintln(os.Stderr, "  cleanup|clean           remove orphaned cached dependencies")
		fmt.Fprintln(os.Stderr, "  check|lint [path]       typecheck file or recursively check folder (.fer only)")
		fmt.Fprintln(os.Stderr, "  run [path] [args]       build and run a program using LLVM")
		colors.CYAN.Fprintln(os.Stderr, "\nExamples:")
		colors.GREEN.Fprintf(os.Stderr, "  %s -backend llvm main.fer\n", os.Args[0])
		colors.GREEN.Fprintf(os.Stderr, "  %s -k main.fer\n", os.Args[0])
		colors.GREEN.Fprintf(os.Stderr, "  %s run main.fer arg1 arg2\n", os.Args[0])
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("v%s\n", compiler.CompilerVersion)
		return
	}
	if *showHelp {
		flag.Usage()
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	selectedBackend := strings.ToLower(strings.TrimSpace(*backendTarget))
	if selectedBackend == "" {
		selectedBackend = "llvm"
	}
	if selectedBackend != string(backend.TargetLLVM) && selectedBackend != string(backend.TargetQBE) {
		colors.RED.Fprintf(os.Stderr, "Error: invalid backend %q (expected llvm or qbe)\n", selectedBackend)
		os.Exit(2)
	}

	result := parsePathWithBackend(flag.Arg(0), selectedBackend, *debugBuild)
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		os.Exit(1)
	}

	if *keepGen {
		if err := emitKeepGenArtifacts(result, selectedBackend, "_gen"); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		colors.GREEN.Fprintln(os.Stdout, "Generated artifacts in _gen")
	}

	if *outputPath != "" {
		if err := buildExecutable(result, *outputPath, backend.Target(selectedBackend)); err != nil {
			colors.RED.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		colors.GREEN.Fprintf(os.Stdout, "Built %s\n", *outputPath)
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

func emitKeepGenArtifacts(result compiler.Result, backendName, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := emitModuleASTDumps(result, outDir); err != nil {
		return err
	}
	if err := emitModuleTextDumps(result, outDir, ".hir", func(mod *context.Module) string {
		if mod.LoweredHIR != nil {
			return hir.FormatModule(mod.LoweredHIR)
		}
		return hir.FormatModule(mod.HIR)
	}); err != nil {
		return err
	}
	if err := emitModuleTextDumps(result, outDir, ".mir", func(mod *context.Module) string {
		return mir.FormatModule(mod.MIR)
	}); err != nil {
		return err
	}
	if err := emitBackendModules(result, backendName, outDir); err != nil {
		return err
	}
	return nil
}

func runCommand(args []string) error {
	sourcePath := ""
	runtimeArgs := []string{}
	if len(args) > 0 {
		sourcePath = args[0]
		runtimeArgs = args[1:]
	}
	resolvedPath, entryPath, selectedByDiscovery, err := resolveRunTarget(sourcePath)
	if err != nil {
		return err
	}
	if selectedByDiscovery {
		colors.CYAN.Fprintf(os.Stderr, "using entry: %s\n", entryPath)
	}

	result := parsePathWithBackend(resolvedPath, string(backend.TargetLLVM), false)

	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		return fmt.Errorf("build failed")
	}

	tempPattern := "ferret-run-*"
	if runtime.GOOS == "windows" {
		tempPattern = "ferret-run-*.exe"
	}
	tempFile, err := os.CreateTemp("", tempPattern)
	if err != nil {
		return fmt.Errorf("create temp output: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return err
	}
	_ = os.Remove(tempPath)

	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(tempPath), ".exe") {
		tempPath += ".exe"
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := buildExecutable(result, tempPath, backend.TargetLLVM); err != nil {
		return err
	}

	cmd := exec.Command(tempPath, runtimeArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("run program: %w", err)
	}
	return nil
}

func resolveRunTarget(path string) (resolvedPath string, entryPath string, selectedByDiscovery bool, err error) {
	if strings.TrimSpace(path) == "" {
		entryPath, err = resolveManifestEntryPath(".")
		if err != nil {
			return "", "", false, err
		}
		return entryPath, entryPath, true, nil
	}

	resolvedPath, err = filepath.Abs(path)
	if err != nil {
		return "", "", false, err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", "", false, err
	}
	if info.IsDir() {
		entryPath, err = resolveManifestEntryPath(resolvedPath)
		if err != nil {
			return "", "", false, err
		}
		return entryPath, entryPath, true, nil
	}
	if !strings.EqualFold(filepath.Ext(resolvedPath), compiler.FerretSourceExt) {
		return "", "", false, fmt.Errorf("unsupported source file extension %q (expected %s)", filepath.Ext(resolvedPath), compiler.FerretSourceExt)
	}
	return resolvedPath, resolvedPath, false, nil
}

func resolveManifestEntryPath(startPath string) (string, error) {
	manifestPath, err := manifest.Find(startPath)
	if err != nil {
		return "", fmt.Errorf("run requires an input file or %s with package.entry", manifest.FileName)
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return "", err
	}
	entry := strings.TrimSpace(file.Package.Entry)
	if entry == "" {
		return "", fmt.Errorf("%s: package.entry is required for `ferret run` without an explicit file", manifestPath)
	}

	entry = strings.ReplaceAll(entry, "\\", "/")
	if filepath.Ext(entry) == "" {
		entry += compiler.FerretSourceExt
	}
	if !strings.EqualFold(filepath.Ext(entry), compiler.FerretSourceExt) {
		return "", fmt.Errorf("%s: package.entry must point to a %s file", manifestPath, compiler.FerretSourceExt)
	}

	manifestDir := filepath.Dir(manifestPath)
	entryPath := filepath.Clean(filepath.Join(manifestDir, filepath.FromSlash(entry)))
	rel, relErr := filepath.Rel(manifestDir, entryPath)
	if relErr != nil {
		return "", relErr
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: package.entry must stay inside the package root", manifestPath)
	}

	entryInfo, statErr := os.Stat(entryPath)
	if statErr != nil {
		return "", fmt.Errorf("entry file not found: %s", entryPath)
	}
	if entryInfo.IsDir() {
		return "", fmt.Errorf("entry path is a directory: %s", entryPath)
	}
	return entryPath, nil
}

func checkCommand(args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	result := parsePathForCheck(path)
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		return fmt.Errorf("check failed")
	}
	return nil
}

func parsePathWithBackend(path, targetBackend string, buildDebug bool) compiler.Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	info, err := os.Stat(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	if info.IsDir() {
		entryPath, entryErr := resolveManifestEntryPath(absPath)
		if entryErr != nil {
			diag.Add(diagnostics.NewError(entryErr.Error()))
			return compiler.Result{Diagnostics: diag}
		}
		ws, err := project.Load(entryPath, compiler.FerretSourceExt)
		if err != nil {
			diag.Add(diagnostics.NewError(err.Error()))
			return compiler.Result{Diagnostics: diag}
		}
		ws.Context.TargetBackend = targetBackend
		ws.Context.BuildDebug = buildDebug
		compiler := compiler.NewWithConfig(ws.Context, diag)
		return compiler.ParseEntry(entryPath)
	}
	if !strings.EqualFold(filepath.Ext(absPath), compiler.FerretSourceExt) {
		diag.Add(diagnostics.NewError(fmt.Sprintf("unsupported source file extension %q (expected %s)", filepath.Ext(absPath), compiler.FerretSourceExt)))
		return compiler.Result{Diagnostics: diag}
	}
	ws, err := project.Load(absPath, compiler.FerretSourceExt)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	ws.Context.TargetBackend = targetBackend
	ws.Context.BuildDebug = buildDebug
	compiler := compiler.NewWithConfig(ws.Context, diag)
	return compiler.ParseEntry(absPath)
}

func parsePathForCheck(path string) compiler.Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	info, err := os.Stat(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	if info.IsDir() {
		ws, err := project.Load(absPath, compiler.FerretSourceExt)
		if err != nil {
			diag.Add(diagnostics.NewError(err.Error()))
			return compiler.Result{Diagnostics: diag}
		}
		compiler := compiler.NewWithConfig(ws.Context, diag)
		return compiler.ParseWorkspace()
	}
	if !strings.EqualFold(filepath.Ext(absPath), compiler.FerretSourceExt) {
		diag.Add(diagnostics.NewError(fmt.Sprintf("unsupported source file extension %q (expected %s)", filepath.Ext(absPath), compiler.FerretSourceExt)))
		return compiler.Result{Diagnostics: diag}
	}
	ws, err := project.Load(absPath, compiler.FerretSourceExt)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	compiler := compiler.NewWithConfig(ws.Context, diag)
	return compiler.ParseEntry(absPath)
}

func emitModuleASTDumps(result compiler.Result, outDir string) error {
	for _, mod := range allModulesForBuild(result) {
		if mod == nil || mod.AST == nil {
			continue
		}
		payload := map[string]any{
			"import_path":  mod.ImportPath,
			"file_path":    mod.FilePath,
			"origin":       string(mod.Origin),
			"phase":        mod.Phase.String(),
			"dependencies": append([]string(nil), mod.Dependencies...),
			"ast":          safeDebugModule(mod.AST),
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal ast dump for %s: %w", mod.ImportPath, err)
		}
		target, err := moduleArtifactPath(mod, outDir, ".ast.json")
		if err != nil {
			return err
		}
		if err := writeTextFile(target, string(data)); err != nil {
			return err
		}
	}
	return nil
}

func emitModuleTextDumps(result compiler.Result, outDir, ext string, render func(*context.Module) string) error {
	for _, mod := range allModulesForBuild(result) {
		if mod == nil {
			continue
		}
		content := render(mod)
		if content == "" {
			continue
		}
		target, err := moduleArtifactPath(mod, outDir, ext)
		if err != nil {
			return err
		}
		if err := writeTextFile(target, content); err != nil {
			return err
		}
	}
	return nil
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

func emitBackendModules(result compiler.Result, targetText, outDir string) error {
	target := backend.Target(strings.ToLower(strings.TrimSpace(targetText)))
	lowerer, err := registry.New(target)
	if err != nil {
		return err
	}
	for _, mod := range allModulesForBuild(result) {
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
		targetPath, err := moduleArtifactPath(mod, outDir, artifact.FileExt)
		if err != nil {
			return err
		}
		if err := writeTextFile(targetPath, artifact.Text); err != nil {
			return err
		}
	}
	return nil
}

func moduleArtifactPath(mod *context.Module, outDir, ext string) (string, error) {
	if mod == nil {
		return "", fmt.Errorf("nil module")
	}
	if outDir == "" {
		return "", fmt.Errorf("empty artifact output directory")
	}
	rel := filepath.FromSlash(mod.ImportPath) + ext
	return filepath.Join(outDir, rel), nil
}

func backendLayouts(result compiler.Result) map[string]*layout.Module {
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

func backendModules(result compiler.Result) map[string]*mir.Module {
	modules := make(map[string]*mir.Module)
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
func buildExecutable(result compiler.Result, outputPath string, target backend.Target) error {
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
func allModulesForBuild(result compiler.Result) []*context.Module {
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

func safeDebugModule(module *ast.Module) any {
	defer func() {
		_ = recover()
	}()
	return ast.DebugModule(module)
}
