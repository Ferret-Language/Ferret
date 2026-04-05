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

	"compiler/internal/backend"
	"compiler/internal/backend/qbe"
	"compiler/internal/core/abi"
)

type bundleTarget struct {
	label string
	run   func() error
}

type bundleContext struct {
	root            string
	coreBundleDir   string
	coreBinDir      string
	coreLibsDir     string
	toolchainDir    string
	toolchainBinDir string
	toolchainLibDir string
	bundledTools    map[string]string
	clangPath       string
	toolBinaryPaths []string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build bundler: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		return fmt.Errorf("this command has no subcommands; use: go run ./bundler")
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	ctx, err := prepareBundleContext(root)
	if err != nil {
		return err
	}

	targets, err := planBundleTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := target.run(); err != nil {
			return fmt.Errorf("%s: %w", target.label, err)
		}
	}

	fmt.Printf("packaged %s and %s\n", ctx.coreBundleDir, ctx.toolchainDir)
	return nil
}

func prepareBundleContext(root string) (bundleContext, error) {
	for _, legacy := range []string{"libs", "bin"} {
		legacyPath := filepath.Join(root, legacy)
		if err := os.RemoveAll(legacyPath); err != nil {
			return bundleContext{}, fmt.Errorf("clean legacy %s: %w", legacyPath, err)
		}
	}

	buildDir := filepath.Join(root, "build")
	if err := os.RemoveAll(buildDir); err != nil {
		return bundleContext{}, fmt.Errorf("clean build dir: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return bundleContext{}, fmt.Errorf("create build dir: %w", err)
	}

	ctx := bundleContext{
		root:            root,
		coreBundleDir:   filepath.Join(buildDir, "core"),
		coreBinDir:      filepath.Join(buildDir, "core", "bin"),
		coreLibsDir:     filepath.Join(buildDir, "core", "libs"),
		toolchainDir:    filepath.Join(buildDir, "toolchain"),
		toolchainBinDir: filepath.Join(buildDir, "toolchain", "bin"),
		toolchainLibDir: filepath.Join(buildDir, "toolchain", "lib"),
	}
	for _, dir := range []string{
		ctx.coreBundleDir,
		ctx.coreBinDir,
		ctx.coreLibsDir,
		ctx.toolchainDir,
		ctx.toolchainBinDir,
		ctx.toolchainLibDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return bundleContext{}, fmt.Errorf("create bundle dir %s: %w", dir, err)
		}
	}

	bundledTools, err := resolveBundledTools()
	if err != nil {
		return bundleContext{}, err
	}
	ctx.bundledTools = bundledTools
	ctx.clangPath = bundledTools[binaryName("clang")]
	ctx.toolBinaryPaths = make([]string, 0, len(bundledTools))
	for _, name := range sortedKeys(bundledTools) {
		ctx.toolBinaryPaths = append(ctx.toolBinaryPaths, bundledTools[name])
	}
	return ctx, nil
}

func planBundleTargets(ctx bundleContext) ([]bundleTarget, error) {
	targets := make([]bundleTarget, 0, 16)
	targets = appendCoreBundleTargets(targets, ctx)
	targets = appendToolchainBundleTargets(targets, ctx)
	switch runtime.GOOS {
	case "linux":
		var err error
		targets, err = appendLinuxBundleTargets(targets, ctx)
		if err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func appendCoreBundleTargets(targets []bundleTarget, ctx bundleContext) []bundleTarget {
	runtimeDir := filepath.Join(ctx.root, "runtime")
	targets = append(targets,
		bundleTarget{
			label: "bundle ferret libs",
			run: func() error {
				return syncFerretLibs(filepath.Join(ctx.root, "ferret_libs_dev"), ctx.coreLibsDir)
			},
		},
		bundleTarget{
			label: "bundle runtime archive",
			run: func() error {
				return buildRuntimeLib(runtimeDir, ctx.coreLibsDir, 0)
			},
		},
		bundleTarget{
			label: "bundle compiler",
			run: func() error {
				return buildCompiler(ctx.root, filepath.Join(ctx.coreBinDir, binaryName("ferret")))
			},
		},
	)

	for _, file := range []string{"README.md", "LICENSE"} {
		src := filepath.Join(ctx.root, file)
		if info, err := os.Stat(src); err == nil && !info.IsDir() {
			dst := filepath.Join(ctx.coreBundleDir, file)
			targets = append(targets, bundleTarget{
				label: fmt.Sprintf("bundle %s", file),
				run: func(src, dst string) func() error {
					return func() error {
						return copyFile(src, dst)
					}
				}(src, dst),
			})
		}
	}

	return targets
}

func appendToolchainBundleTargets(targets []bundleTarget, ctx bundleContext) []bundleTarget {
	for _, name := range sortedKeys(ctx.bundledTools) {
		src := ctx.bundledTools[name]
		dst := filepath.Join(ctx.toolchainBinDir, name)
		targets = append(targets, bundleTarget{
			label: fmt.Sprintf("bundle tool %s", name),
			run: func(src, dst string) func() error {
				return func() error {
					return copyFile(src, dst)
				}
			}(src, dst),
		})
	}

	targets = append(targets,
		bundleTarget{
			label: "bundle toolchain shared libraries",
			run: func() error {
				return copyToolchainSharedLibraries(ctx.toolchainLibDir, ctx.toolBinaryPaths)
			},
		},
		bundleTarget{
			label: "bundle clang resources",
			run: func() error {
				resourceDir, err := clangResourceDir(ctx.clangPath)
				if err != nil {
					return err
				}
				return copyDirContents(resourceDir, filepath.Join(ctx.toolchainLibDir, "clang", filepath.Base(resourceDir)))
			},
		},
	)
	return targets
}

func appendLinuxBundleTargets(targets []bundleTarget, ctx bundleContext) ([]bundleTarget, error) {
	runtimeDir := filepath.Join(ctx.root, "runtime")
	targets = append(targets, bundleTarget{
		label: "bundle runtime archive (32-bit)",
		run: func() error {
			return buildRuntimeLib(runtimeDir, ctx.coreLibsDir, abi.Bits32)
		},
	})

	lib32Copies, err := linuxLib32CopyTargets(ctx.clangPath, ctx.toolchainDir)
	if err != nil {
		return nil, err
	}
	targets = append(targets, lib32Copies...)
	return targets, nil
}

func linuxLib32CopyTargets(clangPath string, toolchainDir string) ([]bundleTarget, error) {
	startFilePath, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=Scrt1.o")
	if err != nil {
		return nil, fmt.Errorf("bundle toolchain 32-bit support: resolve start files: %w", err)
	}
	libgccPath, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=libgcc.a")
	if err != nil {
		return nil, fmt.Errorf("bundle toolchain 32-bit support: resolve libgcc dir: %w", err)
	}
	stubs32Path, err := linuxStubs32Header(clangPath)
	if err != nil {
		return nil, err
	}

	return []bundleTarget{
		{
			label: "bundle toolchain 32-bit start files",
			run: func() error {
				return copyDirContents(filepath.Dir(startFilePath), filepath.Join(toolchainDir, "lib32"))
			},
		},
		{
			label: "bundle toolchain 32-bit libgcc",
			run: func() error {
				return copyDirContents(filepath.Dir(libgccPath), filepath.Join(toolchainDir, "lib32"))
			},
		},
		{
			label: "bundle toolchain 32-bit headers",
			run: func() error {
				return copyDirContents(filepath.Dir(stubs32Path), filepath.Join(toolchainDir, "include", "gnu"))
			},
		},
	}, nil
}

func resolveBundledTools() (map[string]string, error) {
	bundled := map[string]string{}

	qbePath, err := qbe.QBEBinary()
	if err != nil {
		return nil, fmt.Errorf("bundle qbe: %w", err)
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
		return nil, fmt.Errorf("bundle toolchain: clang is required in PATH for release packaging")
	}
	return bundled, nil
}

func clangResourceDir(clangPath string) (string, error) {
	out, err := exec.Command(clangPath, "-print-resource-dir").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bundle clang resources: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	resourceDir := filepath.Clean(strings.TrimSpace(string(out)))
	info, err := os.Stat(resourceDir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("bundle clang resources: invalid resource dir %s", resourceDir)
	}
	return resourceDir, nil
}

func clangResolvedFile(clangPath string, args ...string) (string, error) {
	out, err := exec.Command(clangPath, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	resolved := filepath.Clean(strings.TrimSpace(string(out)))
	if resolved == "" || resolved == filepath.Base(args[len(args)-1]) || !isFile(resolved) {
		return "", fmt.Errorf("missing %s from toolchain", strings.TrimPrefix(args[len(args)-1], "-print-file-name="))
	}
	return resolved, nil
}

func linuxStubs32Header(clangPath string) (string, error) {
	out, err := exec.Command(clangPath, "-m32", "-E", "-v", "-x", "c", "/dev/null", "-o", "/dev/null").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bundle toolchain 32-bit support: resolve include search dirs: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	inSearchList := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "#include <...> search starts here:" {
			inSearchList = true
			continue
		}
		if trimmed == "End of search list." {
			break
		}
		if !inSearchList || trimmed == "" {
			continue
		}
		candidate := filepath.Join(trimmed, "gnu", "stubs-32.h")
		if isFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("bundle toolchain 32-bit support: missing gnu/stubs-32.h in toolchain include search paths")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func buildRuntimeLib(runtimeDir, libsDir string, bits int) error {
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

func copyDirContents(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dstDir, rel))
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
