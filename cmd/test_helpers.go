package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"compiler/colors"
	"compiler/internal/backend"
	"compiler/internal/core/context"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
)

type testRunResult struct {
	Name    string
	Passed  bool
	Output  string
	Elapsed time.Duration
}

type testFailureDetails struct {
	Message  string
	Expected string
	Got      string
	Raw      string
	Known    bool
}

const (
	testFailMarker     = "__FERRET_TEST_FAIL__"
	testMessageMarker  = "__FERRET_TEST_MESSAGE__"
	testExpectedMarker = "__FERRET_TEST_EXPECTED__"
	testGotMarker      = "__FERRET_TEST_GOT__"
)

func countModuleTests(mod *context.Module) int {
	return len(moduleTests(mod))
}

func moduleTests(mod *context.Module) []*ast.FuncDecl {
	if mod == nil || mod.AST == nil {
		return nil
	}
	tests := make([]*ast.FuncDecl, 0)
	for _, decl := range mod.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn != nil && fn.IsTest {
			tests = append(tests, fn)
		}
	}
	return tests
}

type testTarget struct {
	FilePath    string
	DisplayPath string
	TestName    string
}

func collectTestTargets(result compiler.Result, resolvedPath string, projectWide bool) []testTarget {
	if projectWide {
		targets := make([]testTarget, 0)
		for _, mod := range result.Modules {
			if mod == nil || mod.Origin != context.ModuleOriginLocal {
				continue
			}
			for _, test := range moduleTests(mod) {
				if test == nil || test.Name == nil {
					continue
				}
				targets = append(targets, testTarget{
					FilePath:    mod.FilePath,
					DisplayPath: displayPath(mod.FilePath),
					TestName:    displayTestName(test),
				})
			}
		}
		slices.SortFunc(targets, func(a, b testTarget) int {
			if a.FilePath != b.FilePath {
				return strings.Compare(a.FilePath, b.FilePath)
			}
			return strings.Compare(a.TestName, b.TestName)
		})
		return targets
	}

	targets := make([]testTarget, 0)
	for _, test := range moduleTests(result.Entry) {
		if test == nil || test.Name == nil {
			continue
		}
		targets = append(targets, testTarget{
			FilePath:    resolvedPath,
			DisplayPath: displayPath(resolvedPath),
			TestName:    displayTestName(test),
		})
	}
	return targets
}

func displayPath(path string) string {
	if relPath, err := filepath.Rel(".", path); err == nil && relPath != "" && relPath != "." {
		return relPath
	}
	return path
}

func runSingleTest(path, testName, displayName string, runtimeArgs []string, target backend.Target) (testRunResult, error) {
	if target == "" {
		target = backend.TargetLLVM
	}
	result := parsePathWithTest(path, testName, target)
	if result.Diagnostics.HasErrors() {
		result.Diagnostics.EmitErrors()
		return testRunResult{}, errAlreadyReported
	}

	tempPattern := "ferret-test-*"
	if runtime.GOOS == "windows" {
		tempPattern = "ferret-test-*.exe"
	}
	tempFile, err := os.CreateTemp("", tempPattern)
	if err != nil {
		return testRunResult{}, fmt.Errorf("create temp output: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return testRunResult{}, err
	}
	_ = os.Remove(tempPath)

	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(tempPath), ".exe") {
		tempPath += ".exe"
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := buildExecutable(result, tempPath, target); err != nil {
		return testRunResult{}, err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(tempPath, runtimeArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)
	output := stdout.String() + stderr.String()
	if err == nil {
		return testRunResult{Name: displayName, Passed: true, Output: output, Elapsed: elapsed}, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return testRunResult{Name: displayName, Passed: false, Output: output, Elapsed: elapsed}, nil
	}
	return testRunResult{}, fmt.Errorf("run test %s: %w", testName, err)
}

func displayTestName(fn *ast.FuncDecl) string {
	if fn == nil {
		return ""
	}
	if strings.TrimSpace(fn.TestName) != "" {
		return fn.TestName
	}
	if fn.Name != nil {
		return fn.Name.Text()
	}
	return ""
}

func parseTestFailureOutput(output string) testFailureDetails {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	details := testFailureDetails{}
	raw := make([]string, 0, len(lines))
	pending := ""

	flushPending := func() {
		if pending == "" {
			return
		}
		switch pending {
		case testMessageMarker:
			details.Message = ""
		case testExpectedMarker:
			details.Expected = ""
		case testGotMarker:
			details.Got = ""
		}
		pending = ""
	}

	for _, line := range lines {
		switch line {
		case testFailMarker:
			details.Known = true
			flushPending()
			continue
		case testMessageMarker, testExpectedMarker, testGotMarker:
			details.Known = true
			flushPending()
			pending = line
			continue
		}

		if pending != "" {
			switch pending {
			case testMessageMarker:
				details.Message = line
			case testExpectedMarker:
				details.Expected = line
			case testGotMarker:
				details.Got = line
			}
			pending = ""
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		raw = append(raw, line)
	}
	flushPending()
	details.Raw = strings.Join(raw, "\n")
	return details
}

func renderTestFailure(name, output string, elapsed time.Duration) {
	printTestStatus(os.Stdout, colors.RED, "FAIL", name, elapsed)
	details := parseTestFailureOutput(output)
	if !details.Known {
		if strings.TrimSpace(output) != "" {
			printIndented(os.Stdout, output)
		}
		return
	}
	if strings.TrimSpace(details.Message) != "" {
		fmt.Fprintf(os.Stdout, "  %s\n", details.Message)
	}
	if strings.TrimSpace(details.Expected) != "" {
		fmt.Fprintf(os.Stdout, "  expected: %s\n", details.Expected)
	}
	if strings.TrimSpace(details.Got) != "" {
		fmt.Fprintf(os.Stdout, "  got:      %s\n", details.Got)
	}
	if strings.TrimSpace(details.Raw) != "" {
		printIndented(os.Stdout, details.Raw)
	}
}

func printTestStatus(w io.Writer, color colors.COLOR, status, name string, elapsed time.Duration) {
	color.Fprintf(w, "    %-5s", status)
	if elapsed < time.Millisecond {
		fmt.Fprintf(w, " %8s  %q\n", elapsed.Round(time.Microsecond), name)
		return
	}
	fmt.Fprintf(w, " %8s  %q\n", elapsed.Round(time.Millisecond), name)
}

func printIndented(w io.Writer, text string) {
	trimmed := strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if trimmed == "" {
		return
	}
	for line := range strings.SplitSeq(trimmed, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}
