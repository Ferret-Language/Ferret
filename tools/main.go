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

	"compiler/internal/backend/qbe"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build tools: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		return fmt.Errorf("this command has no subcommands; use: go run ./tools")
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	for _, legacy := range []string{"libs", "bin"} {
		legacyPath := filepath.Join(root, legacy)
		if err := os.RemoveAll(legacyPath); err != nil {
			return fmt.Errorf("clean legacy %s: %w", legacyPath, err)
		}
	}

	buildDir := filepath.Join(root, "build")
	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("clean build dir: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	coreBundlePath := filepath.Join(buildDir, "core")
	if err := createCoreBundle(root, coreBundlePath); err != nil {
		return err
	}

	toolchainBundlePath := filepath.Join(buildDir, "toolchain")
	if err := createToolchainBundle(toolchainBundlePath); err != nil {
		return err
	}
	fmt.Printf("packaged %s and %s\n", coreBundlePath, toolchainBundlePath)
	return nil
}

func createCoreBundle(root, bundleDir string) error {
	if err := os.RemoveAll(bundleDir); err != nil {
		return fmt.Errorf("clean bundle dir: %w", err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}

	bundleBinDir := filepath.Join(bundleDir, "bin")
	bundleLibsDir := filepath.Join(bundleDir, "libs")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		return fmt.Errorf("create bundle bin dir: %w", err)
	}
	if err := os.MkdirAll(bundleLibsDir, 0o755); err != nil {
		return fmt.Errorf("create bundle libs dir: %w", err)
	}

	if err := syncFerretLibs(filepath.Join(root, "ferret_libs_dev"), bundleLibsDir); err != nil {
		return err
	}
	if err := buildRuntimeLib(filepath.Join(root, "runtime"), bundleLibsDir); err != nil {
		return err
	}
	if err := buildCompiler(root, filepath.Join(bundleBinDir, binaryName("ferret"))); err != nil {
		return err
	}

	for _, file := range []string{"README.md", "LICENSE"} {
		src := filepath.Join(root, file)
		if info, err := os.Stat(src); err == nil && !info.IsDir() {
			if err := copyFile(src, filepath.Join(bundleDir, file)); err != nil {
				return fmt.Errorf("copy %s: %w", file, err)
			}
		}
	}

	return nil
}

func createToolchainBundle(bundleDir string) error {
	if err := os.RemoveAll(bundleDir); err != nil {
		return fmt.Errorf("clean toolchain bundle dir: %w", err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return fmt.Errorf("create toolchain bundle dir: %w", err)
	}

	toolchainBinDir := filepath.Join(bundleDir, "bin")
	toolchainLibDir := filepath.Join(bundleDir, "lib")
	if err := os.MkdirAll(toolchainBinDir, 0o755); err != nil {
		return fmt.Errorf("create toolchain/bin dir: %w", err)
	}
	if err := os.MkdirAll(toolchainLibDir, 0o755); err != nil {
		return fmt.Errorf("create toolchain/lib dir: %w", err)
	}

	bundled := map[string]string{}

	qbePath, err := qbe.QBEBinary()
	if err != nil {
		return fmt.Errorf("bundle qbe: %w", err)
	}
	bundled[binaryName("qbe")] = qbePath

	for _, name := range []string{"clang", "clang++", "ld.lld", "lld", "cc", "gcc"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		base := filepath.Base(path)
		if _, exists := bundled[base]; exists {
			continue
		}
		bundled[base] = path
	}

	if _, ok := bundled[binaryName("clang")]; !ok {
		return fmt.Errorf("bundle toolchain: clang is required in PATH for release packaging")
	}

	keys := make([]string, 0, len(bundled))
	for name := range bundled {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if err := copyFile(bundled[name], filepath.Join(toolchainBinDir, name)); err != nil {
			return fmt.Errorf("copy tool %s: %w", name, err)
		}
	}

	binaries := make([]string, 0, len(keys))
	for _, name := range keys {
		binaries = append(binaries, filepath.Join(toolchainBinDir, name))
	}
	if err := copyToolchainSharedLibraries(toolchainLibDir, binaries); err != nil {
		return err
	}

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

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyToolchainSharedLibraries(toolchainLibDir string, binaries []string) error {
	deps := make(map[string]struct{})
	for _, bin := range binaries {
		libs, err := sharedLibraryDependencies(bin)
		if err != nil {
			return fmt.Errorf("toolchain deps for %s: %w", filepath.Base(bin), err)
		}
		for _, lib := range libs {
			deps[lib] = struct{}{}
		}
	}

	paths := make([]string, 0, len(deps))
	for path := range deps {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, src := range paths {
		if skipBundledSharedLibrary(src) {
			continue
		}
		dst := filepath.Join(toolchainLibDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy toolchain shared lib %s: %w", src, err)
		}
	}
	return nil
}

func sharedLibraryDependencies(binary string) ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		return dependenciesFromOtool(binary)
	default:
		return dependenciesFromLdd(binary)
	}
}

func dependenciesFromLdd(binary string) ([]string, error) {
	cmd := exec.Command("ldd", binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ldd failed: %w\n%s", err, out)
	}

	deps := make([]string, 0)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "linux-vdso") {
			continue
		}
		if strings.Contains(line, "=> not found") {
			return nil, fmt.Errorf("missing runtime dependency: %s", line)
		}

		candidate := ""
		if idx := strings.Index(line, "=>"); idx >= 0 {
			rhs := strings.TrimSpace(line[idx+2:])
			fields := strings.Fields(rhs)
			if len(fields) > 0 {
				candidate = fields[0]
			}
		} else if strings.HasPrefix(line, "/") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				candidate = fields[0]
			}
		}
		if !strings.HasPrefix(candidate, "/") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		if info, statErr := os.Stat(candidate); statErr != nil || info.IsDir() {
			continue
		}
		seen[candidate] = struct{}{}
		deps = append(deps, candidate)
	}
	return deps, nil
}

func skipBundledSharedLibrary(path string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	base := filepath.Base(path)
	switch base {
	case "libc.so.6", "libm.so.6", "libpthread.so.0", "librt.so.1", "libdl.so.2", "libresolv.so.2", "libutil.so.1", "libnsl.so.1", "libcrypt.so.1":
		return true
	}
	return strings.HasPrefix(base, "ld-linux")
}

func dependenciesFromOtool(binary string) ([]string, error) {
	cmd := exec.Command("otool", "-L", binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("otool failed: %w\n%s", err, out)
	}

	deps := make([]string, 0)
	seen := map[string]struct{}{}
	lines := strings.Split(string(out), "\n")
	for idx, line := range lines {
		if idx == 0 {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		candidate := fields[0]
		if !strings.HasPrefix(candidate, "/") {
			continue
		}
		if strings.HasPrefix(candidate, "/usr/lib/") || strings.HasPrefix(candidate, "/System/Library/") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		if info, statErr := os.Stat(candidate); statErr != nil || info.IsDir() {
			continue
		}
		seen[candidate] = struct{}{}
		deps = append(deps, candidate)
	}
	return deps, nil
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
