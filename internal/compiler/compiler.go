package compiler

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/pipeline"
	"compiler/internal/stdlib"
	"compiler/internal/utils/fs"
)

type FORMAT int

const (
	ANSI FORMAT = iota
	HTML
)

const Version = "0.0.5"

// Options for compilation
type Options struct {
	// For file-based compilation
	EntryFile string
	// For in-memory compilation (WASM)
	Files map[string]string // map[filename]content - e.g. {"main.fer": "...", "utils.fer": "..."}
	// Debug output
	Debug   bool
	SaveAST bool
	// Output format: "ansi" or "html"
	LogFormat FORMAT
	// Output executable path (if empty, uses default: <entryDir>/<projectName>)
	OutputExecutable string
	// Keep generated files after compilation
	KeepGenFiles bool

	// Skip codegen (stop after type checking)
	TypecheckOnly bool

	// Codegen backend ("wasm", "qbe")
	CodegenBackend string
}

// Result of compilation
type Result struct {
	Success  bool
	Output   string
	Artifact []byte
}

// Compile compiles Ferret code and returns the result
func Compile(opts *Options) Result {
	// Setup compiler config
	projectName := "playground"
	projectRoot := "/playground"

	if opts.EntryFile != "" {
		absPath, err := filepath.Abs(opts.EntryFile)
		if err != nil {
			return Result{Success: false, Output: fmt.Sprintf("Failed to resolve path: %v", err)}
		}

		if !fs.IsValidFile(absPath) {
			return Result{Success: false, Output: fmt.Sprintf("Invalid file path: %v", absPath)}
		}

		entryDir := filepath.Dir(absPath)
		projectName = filepath.Base(entryDir)
		projectRoot = entryDir
	}

	config := &context_v2.Config{
		ProjectName:    projectName,
		ProjectRoot:    projectRoot,
		Extension:      ".fer",
		RuntimePath:    fs.ResolveLibsPath(), // Resolved by build.go relative to ferret binary
		OutputPath:     "",                   // Will be set after loading manifest
		SaveAST:        opts.SaveAST,
		KeepGenFiles:   opts.KeepGenFiles,
		TypeCheckOnly:  opts.TypecheckOnly,
		CodegenBackend: opts.CodegenBackend,
		PointerSize:    0,
	}
	if opts.CodegenBackend == "wasm" {
		config.PointerSize = 4
	}

	ctx := context_v2.New(config, opts.Debug)
	// Load embedded stdlib for WASM builds and in-memory compilation
	if opts.CodegenBackend == "wasm" || opts.Files != nil {
		stdlib.LoadEmbeddedBuiltins(ctx)
	}

	// Set entry point
	var err error
	if opts.Files != nil {
		// In-memory mode
		err = ctx.SetEntryPointWithFiles(opts.Files, "main")
	} else {
		// File-based mode
		err = ctx.SetEntryPoint(opts.EntryFile)
	}

	if err != nil {
		return Result{Success: false, Output: fmt.Sprintf("Failed to set entry point: %v", err)}
	}

	// Determine output path after loading manifest (so we can use package name)
	outputPath := opts.OutputExecutable
	if outputPath == "" {
		// Use package name from manifest if available, otherwise use directory name
		finalProjectName := ctx.Config.ProjectName
		outputPath = filepath.Join(ctx.Config.ProjectRoot, finalProjectName)
	}
	if opts.CodegenBackend == "wasm" && !strings.HasSuffix(strings.ToLower(outputPath), ".wasm") {
		outputPath += ".wasm"
	}
	// Ensure .exe suffix on Windows for native binaries
	if opts.CodegenBackend != "wasm" && runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(outputPath), ".exe") {
		outputPath += ".exe"
	}
	ctx.Config.OutputPath = outputPath

	// Run pipeline
	p := pipeline.New(ctx)
	if err := p.Run(); err != nil && !ctx.HasErrors() {
		ctx.ReportError(err.Error(), nil)
	}

	// Emit diagnostics and return result
	if opts.LogFormat == HTML {
		output := ctx.Diagnostics.EmitAllToString()
		return Result{Success: !ctx.HasErrors(), Output: colors.ConvertANSIToHTML(output), Artifact: ctx.CodegenOutput}
	}

	ctx.EmitDiagnostics()
	return Result{Success: !ctx.HasErrors(), Artifact: ctx.CodegenOutput}
}
