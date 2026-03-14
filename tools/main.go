package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build tools: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	libsDir := filepath.Join(root, "libs")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(libsDir, 0o755); err != nil {
		return fmt.Errorf("create libs dir: %w", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	if err := syncFerretLibs(filepath.Join(root, "ferret_libs_dev"), libsDir); err != nil {
		return err
	}
	if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir); err != nil {
		return err
	}
	if err := buildCompiler(root, filepath.Join(binDir, binaryName("ferret"))); err != nil {
		return err
	}

	fmt.Printf("built %s\n", filepath.Join(binDir, binaryName("ferret")))
	return nil
}

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

func syncFerretLibs(srcDir, dstDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("stat ferret_libs_dev: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ferret_libs_dev is not a directory: %s", srcDir)
	}
	if err := os.RemoveAll(dstDir); err != nil {
		return fmt.Errorf("clean libs dir: %w", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("recreate libs dir: %w", err)
	}
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
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

func buildRuntimeLib(runtimeDir, libsDir string) error {
	entries, err := filepath.Glob(filepath.Join(runtimeDir, "*.c"))
	if err != nil {
		return fmt.Errorf("scan runtime sources: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no runtime C files found in %s", runtimeDir)
	}
	sort.Strings(entries)

	cc, err := resolveCCompiler()
	if err != nil {
		return err
	}
	ar, err := resolveTool("ar")
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
		if runtime.GOOS == "linux" {
			args = append(args, "-fPIC")
		}
		if err := runCmd("", cc, args...); err != nil {
			return fmt.Errorf("compile runtime %s: %w", filepath.Base(src), err)
		}
		objFiles = append(objFiles, obj)
	}

	libPath := filepath.Join(libsDir, "ferret_runtime.a")
	args := append([]string{"rcs", libPath}, objFiles...)
	if err := runCmd("", ar, args...); err != nil {
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
	return runCmd(root, "go", "build", "-o", outPath, "./cmd/ferret")
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

func resolveTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	return path, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func binaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
