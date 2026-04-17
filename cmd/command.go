package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"compiler/colors"
	"compiler/internal/backend"
	"compiler/internal/core/abi"
	"compiler/internal/core/manifest"
	compiler "compiler/internal/driver"
)

var errAlreadyReported = errors.New("diagnostics already reported")

func parseCommandArgs(name string, args []string) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	logFormat := fs.String("logformat", string(colors.LogFormatANSI), "log output format (ansi|normal|html)")
	m32 := fs.Bool("m32", false, "target 32-bit ABI")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := colors.SetLogFormatString(*logFormat); err != nil {
		return nil, err
	}
	if *m32 {
		if err := abi.SetSizeBits(abi.Bits32); err != nil {
			return nil, err
		}
	} else if err := abi.SetSizeBits(0); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

func parseCommandBackend(command string) (string, backend.Target, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", fmt.Errorf("empty command")
	}
	base, suffix, hasSuffix := strings.Cut(command, ":")
	switch base {
	case "run", "test":
		if !hasSuffix || strings.TrimSpace(suffix) == "" {
			return base, backend.TargetLLVM, nil
		}
		target := backend.Target(strings.ToLower(strings.TrimSpace(suffix)))
		switch target {
		case backend.TargetLLVM:
			return base, target, nil
		default:
			return "", "", fmt.Errorf("invalid %s backend %q (expected llvm)", base, suffix)
		}
	default:
		return command, "", nil
	}
}

func runCommand(args []string, target backend.Target) error {
	parsedArgs, err := parseCommandArgs("run", args)
	if err != nil {
		return err
	}

	sourcePath := ""
	runtimeArgs := []string{}
	if len(parsedArgs) > 0 {
		sourcePath = parsedArgs[0]
		runtimeArgs = parsedArgs[1:]
	}
	resolvedPath, entryPath, selectedByDiscovery, err := resolveRunTarget(sourcePath)
	if err != nil {
		return err
	}
	if selectedByDiscovery {
		colors.CYAN.Fprintf(os.Stderr, "using entry: %s\n", entryPath)
	}

	if target == "" {
		target = backend.TargetLLVM
	}
	result := parsePathWithBackend(resolvedPath, string(target), false)

	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		return errAlreadyReported
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

	if err := buildExecutable(result, tempPath, target); err != nil {
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

func testCommand(args []string, target backend.Target) error {
	parsedArgs, err := parseCommandArgs("test", args)
	if err != nil {
		return err
	}

	sourcePath := ""
	runtimeArgs := []string{}
	if len(parsedArgs) > 0 {
		sourcePath = parsedArgs[0]
		runtimeArgs = parsedArgs[1:]
	}
	resolvedPath, entryPath, selectedByDiscovery, err := resolveRunTarget(sourcePath)
	if err != nil {
		return err
	}
	if selectedByDiscovery {
		colors.CYAN.Fprintf(os.Stderr, "using entry: %s\n", entryPath)
	}

	if target == "" {
		target = backend.TargetLLVM
	}
	result := parsePathWithTest(resolvedPath, "", target)
	if selectedByDiscovery {
		result = parseWorkspaceWithConfig(filepath.Dir(resolvedPath), string(target))
	}
	if result.Diagnostics.HasErrors() {
		result.Diagnostics.EmitErrors()
		return errAlreadyReported
	}
	testTargets := collectTestTargets(result, resolvedPath, selectedByDiscovery)
	if len(testTargets) == 0 {
		return fmt.Errorf("no tests found in %s", resolvedPath)
	}
	colors.CYAN.Fprintf(os.Stderr, "running %d test(s)\n", len(testTargets))

	passed := 0
	failed := 0
	currentFile := ""
	for _, test := range testTargets {
		if test.FilePath != currentFile {
			fmt.Fprintln(os.Stdout, test.DisplayPath)
			currentFile = test.FilePath
		}
		runResult, err := runSingleTest(test.FilePath, test.TestName, test.TestName, runtimeArgs, target)
		if err != nil {
			return err
		}
		if runResult.Passed {
			passed++
			printTestStatus(os.Stdout, colors.GREEN, "OK", runResult.Name, runResult.Elapsed)
			continue
		}
		failed++
		renderTestFailure(runResult.Name, runResult.Output, runResult.Elapsed)
	}
	fmt.Fprintf(os.Stdout, "\nSummary: %d passed, %d failed, %d total\n", passed, failed, len(testTargets))
	if failed > 0 {
		return fmt.Errorf("%d test(s) failed", failed)
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
	if ext := filepath.Ext(resolvedPath); ext != "" && !strings.EqualFold(ext, compiler.FerretSourceExt) {
		return "", "", false, fmt.Errorf("unsupported source file extension %q (expected %s)", ext, compiler.FerretSourceExt)
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
	parsedArgs, err := parseCommandArgs("check", args)
	if err != nil {
		return err
	}

	path := "."
	if len(parsedArgs) > 0 {
		path = parsedArgs[0]
	}

	result := parsePathForCheck(path)
	if diags := result.Diagnostics.Diagnostics(); len(diags) > 0 {
		result.Diagnostics.EmitAll()
	}
	if result.Diagnostics.HasErrors() {
		return errAlreadyReported
	}
	return nil
}
