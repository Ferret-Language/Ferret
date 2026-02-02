package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"compiler/colors"
	"compiler/internal/context_v2"
	"compiler/internal/pipeline"
	"compiler/internal/utils/fs"
)

type FORMAT int

const (
	ANSI FORMAT = iota
	HTML
)

const Version = "0.0.3"

// Options for compilation
type Options struct {
	// For file-based compilation
	EntryFile string
	// For in-memory compilation (WASM)
	Code string // Single file (deprecated - use Files instead)
	// For multi-file in-memory compilation (WASM)
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

	// Codegen backend ("none", "qbe")
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

	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	libsPath := filepath.Join(execDir, "../libs")
	if !fs.IsDir(libsPath) {
		if cwd, err := os.Getwd(); err == nil {
			candidate := filepath.Join(cwd, "libs")
			if fs.IsDir(candidate) {
				libsPath = candidate
			}
		}
	}

	// Determine output path
	outputPath := opts.OutputExecutable
	if outputPath == "" {
		outputPath = filepath.Join(projectRoot, projectName)
	}
	if opts.CodegenBackend == "wasm" && !strings.HasSuffix(strings.ToLower(outputPath), ".wasm") {
		outputPath += ".wasm"
	}
	// Ensure .exe suffix on Windows for native binaries
	if opts.CodegenBackend != "wasm" && runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(outputPath), ".exe") {
		outputPath += ".exe"
	}

	config := &context_v2.Config{
		ProjectName:    projectName,
		ProjectRoot:    projectRoot,
		Extension:      ".fer",
		RuntimePath:    libsPath, // Runtime/stdlib library path relative to executable
		OutputPath:     outputPath,
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
	if opts.CodegenBackend == "wasm" || opts.Code != "" || opts.Files != nil {
		loadEmbeddedBuiltins(ctx)
	}

	// Set entry point
	var err error
	if opts.Files != nil {
		// Multi-file mode (preferred for WASM)
		err = ctx.SetEntryPointWithFiles(opts.Files, "main")
	} else if opts.Code != "" {
		// Single-file mode (backward compatibility)
		err = ctx.SetEntryPointWithCode(opts.Code, "main")
	} else {
		// File-based mode
		err = ctx.SetEntryPoint(opts.EntryFile)
	}

	if err != nil {
		ctx.ReportError(fmt.Sprintf("Failed to set entry point: %v", err), nil)
		if opts.LogFormat == HTML {
			output := ctx.Diagnostics.EmitAllToString()
			return Result{Success: false, Output: colors.ConvertANSIToHTML(output), Artifact: ctx.CodegenOutput}
		}
		ctx.EmitDiagnostics()
		return Result{Success: false, Artifact: ctx.CodegenOutput}
	}

	// Run pipeline
	p := pipeline.New(ctx)
	p.Run()

	// Emit diagnostics and return result
	if opts.LogFormat == HTML {
		output := ctx.Diagnostics.EmitAllToString()
		return Result{Success: !ctx.HasErrors(), Output: colors.ConvertANSIToHTML(output), Artifact: ctx.CodegenOutput}
	}

	ctx.EmitDiagnostics()
	return Result{Success: !ctx.HasErrors(), Artifact: ctx.CodegenOutput}
}
