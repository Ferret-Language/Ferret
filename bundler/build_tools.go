package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"compiler/internal/backend"
	"compiler/internal/core/abi"
	"compiler/internal/core/context"
)

func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if isFile(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", fmt.Errorf("go.mod not found from %s", cwd)
}

func resetDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func syncFerretLibs(srcDir, dstDir string) error {
	if !isDir(srcDir) {
		return fmt.Errorf("%s is not a directory: %s", context.STD_LIB_DEV, srcDir)
	}
	if err := resetDir(dstDir); err != nil {
		return fmt.Errorf("prepare libs dir: %w", err)
	}
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		if entry.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if filepath.Ext(path) == ".go" {
			return nil
		}
		return copyFile(path, dstPath)
	})
}

func buildRuntimeLib(runtimeDir, libsDir string, bits int) error {
	entries, err := filepath.Glob(filepath.Join(runtimeDir, "*.c"))
	if err != nil {
		return fmt.Errorf("scan runtime sources: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no runtime C files found in %s", runtimeDir)
	}
	sort.Strings(entries)

	cc, err := resolveRuntimeCompiler(bits)
	if err != nil {
		return err
	}
	ar, err := resolveRuntimeArchiver(bits)
	if err != nil {
		return err
	}

	objDir, err := os.MkdirTemp("", "ferret-runtime-*")
	if err != nil {
		return fmt.Errorf("create temp object dir: %w", err)
	}
	defer os.RemoveAll(objDir)

	objFiles := make([]string, 0, len(entries))
	for _, src := range entries {
		obj := filepath.Join(objDir, strings.TrimSuffix(filepath.Base(src), ".c")+".o")
		args := []string{"-std=c11", "-O2", "-Wall", "-Wextra", "-I", runtimeDir, "-c", src, "-o", obj}
		if runtime.GOOS != "windows" {
			args = append(args, "-pthread")
		}
		if bits == abi.Bits32 {
			args = append(args[:1], append([]string{"-m32"}, args[1:]...)...)
		}
		if runtime.GOOS == "linux" {
			args = append(args, "-fPIC")
		}
		if err := runCmd("", cc, args...); err != nil {
			return fmt.Errorf("compile runtime %s: %w", filepath.Base(src), err)
		}
		objFiles = append(objFiles, obj)
	}

	libPath := filepath.Join(libsDir, backend.RuntimeStaticLibName(bits))
	if err := runCmd("", ar, append([]string{"rcs", libPath}, objFiles...)...); err != nil {
		return fmt.Errorf("archive runtime library: %w", err)
	}
	if ranlib, err := exec.LookPath("ranlib"); err == nil {
		if err := runCmd("", ranlib, libPath); err != nil {
			return fmt.Errorf("ranlib runtime library: %w", err)
		}
	}
	return nil
}

func buildCompiler(root, outPath string) error {
	return runCmd(root, "go", "build", "-o", outPath, "./cmd")
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func resolveCCompiler() (string, error) {
	candidates := []string{"cc", "gcc", "clang"}
	if runtime.GOOS == "darwin" {
		candidates = []string{"clang", "cc", "gcc"}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no C compiler found in PATH (tried %s)", strings.Join(candidates, ", "))
}

func resolveRuntimeCompiler(bits int) (string, error) {
	if runtime.GOOS == "windows" {
		if bits == abi.Bits32 {
			return resolveTool("i686-w64-mingw32-gcc", "gcc")
		}
		return resolveTool("x86_64-w64-mingw32-gcc", "gcc")
	}
	return resolveCCompiler()
}

func resolveRuntimeArchiver(bits int) (string, error) {
	if runtime.GOOS == "windows" {
		if bits == abi.Bits32 {
			return resolveTool("i686-w64-mingw32-ar", "ar")
		}
		return resolveTool("x86_64-w64-mingw32-ar", "ar")
	}
	return resolveTool("ar")
}

func resolveTool(names ...string) (string, error) {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("tool not found: %s", strings.Join(names, ", "))
}
