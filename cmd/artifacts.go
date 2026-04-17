package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/backend"
	"compiler/internal/backend/llvm"
	"compiler/internal/backend/registry"
	"compiler/internal/core/context"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/hir"
	"compiler/internal/ir/mir"
)

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
	for _, mod := range compilationModules(result) {
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
	for _, mod := range compilationModules(result) {
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

	if target != backend.TargetLLVM {
		return fmt.Errorf("build: unsupported backend target %q (only llvm is supported)", target)
	}

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
}

// allModulesForBuild returns all modules ordered so imports come before the
// entry module. Avoids duplicates.
func allModulesForBuild(result compiler.Result) []*context.Module {
	seen := make(map[string]struct{})
	modules := compilationModules(result)
	all := make([]*context.Module, 0, len(modules)+1)
	for _, mod := range modules {
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

func compilationModules(result compiler.Result) []*context.Module {
	if result.CompilerState != nil {
		return result.CompilerState.Modules()
	}
	return result.Modules
}

func safeDebugModule(module *ast.Module) any {
	defer func() {
		_ = recover()
	}()
	return ast.DebugModule(module)
}
