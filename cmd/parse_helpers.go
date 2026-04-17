package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compiler/internal/backend"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/project"
	compiler "compiler/internal/driver"
)

func parsePathWithBackend(path, targetBackend string, buildDebug bool) compiler.Result {
	return parsePathWithConfig(path, targetBackend, buildDebug, false, "")
}

func parsePathForTest(path string) compiler.Result {
	return parsePathWithTest(path, "", backend.TargetLLVM)
}

func parseWorkspaceWithConfig(path, targetBackend string) compiler.Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	ws, err := project.Load(absPath, compiler.FerretSourceExt)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	ws.Context.TargetBackend = targetBackend
	compiler := compiler.NewWithConfig(ws.Context, diag)
	return compiler.ParseWorkspace()
}

func parsePathWithTest(path, testName string, target backend.Target) compiler.Result {
	if target == "" {
		target = backend.TargetLLVM
	}
	return parsePathWithConfig(path, string(target), false, true, testName)
}

func parsePathWithConfig(path, targetBackend string, buildDebug, testMode bool, testName string) compiler.Result {
	absPath, err := filepath.Abs(path)
	diag := diagnostics.NewDiagnosticBag(absPath)
	if err != nil {
		diag.Add(diagnostics.NewError(err.Error()))
		return compiler.Result{Diagnostics: diag}
	}
	if ext := filepath.Ext(absPath); ext != "" && !strings.EqualFold(ext, compiler.FerretSourceExt) {
		diag.Add(diagnostics.NewError(fmt.Sprintf("unsupported source file extension %q (expected %s)", ext, compiler.FerretSourceExt)))
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
		ws.Context.TestMode = testMode
		ws.Context.TestName = testName
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
	ws.Context.TestMode = testMode
	ws.Context.TestName = testName
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
	if ext := filepath.Ext(absPath); ext != "" && !strings.EqualFold(ext, compiler.FerretSourceExt) {
		diag.Add(diagnostics.NewError(fmt.Sprintf("unsupported source file extension %q (expected %s)", ext, compiler.FerretSourceExt)))
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
