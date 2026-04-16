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
	"compiler/internal/core/context"
)

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
	buildDir := filepath.Join(root, "build")
	if err := resetDir(buildDir); err != nil {
		return fmt.Errorf("prepare build dir: %w", err)
	}
	for _, legacy := range []string{"libs", "bin"} {
		if err := os.RemoveAll(filepath.Join(root, legacy)); err != nil {
			return fmt.Errorf("clean legacy %s: %w", legacy, err)
		}
	}

	coreDir := filepath.Join(buildDir, "core")
	toolchainDir := filepath.Join(buildDir, "toolchain")
	tools, err := resolveToolBinaries()
	if err != nil {
		return err
	}
	bundle32Bit, err := supportsBundled32BitTarget(tools)
	if err != nil {
		return err
	}
	if err := bundleCore(root, coreDir, bundle32Bit); err != nil {
		return err
	}
	if err := bundleToolchain(toolchainDir, tools, bundle32Bit); err != nil {
		return err
	}

	fmt.Printf("packaged\n\t- %s\n\t- %s\n", coreDir, toolchainDir)
	return nil
}

func bundleCore(root, bundleDir string, bundle32Bit bool) error {
	binDir := filepath.Join(bundleDir, "bin")
	libsDir := filepath.Join(bundleDir, "libs")
	for _, dir := range []string{bundleDir, binDir, libsDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	if err := syncFerretLibs(filepath.Join(root, context.STD_LIB_DEV), libsDir); err != nil {
		return err
	}
	if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir, 0); err != nil {
		return err
	}
	if bundle32Bit {
		if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir, abi.Bits32); err != nil {
			return err
		}
	}
	if err := buildCompiler(root, filepath.Join(binDir, binaryName("ferret"))); err != nil {
		return err
	}
	for _, name := range []string{"README.md", "LICENSE"} {
		src := filepath.Join(root, name)
		if isFile(src) {
			if err := copyFile(src, filepath.Join(bundleDir, name)); err != nil {
				return fmt.Errorf("copy %s: %w", name, err)
			}
		}
	}
	return nil
}

func bundleToolchain(bundleDir string, tools map[string]string, bundle32Bit bool) error {
	binDir := filepath.Join(bundleDir, "bin")
	libDir := filepath.Join(bundleDir, "lib")
	for _, dir := range []string{bundleDir, binDir, libDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	for _, name := range sortedKeys(tools) {
		if err := copyFile(tools[name], filepath.Join(binDir, name)); err != nil {
			return fmt.Errorf("copy tool %s: %w", name, err)
		}
	}

	clangPath := tools[binaryName("clang")]
	if err := copyClangResources(clangPath, libDir); err != nil {
		return err
	}
	if err := copyPlatformToolchainRuntime(bundleDir, libDir, toolPaths(tools), clangPath); err != nil {
		return err
	}
	if runtime.GOOS == "linux" && bundle32Bit {
		if err := copyLinux32ToolchainSupport(clangPath, bundleDir); err != nil {
			return err
		}
	}
	return nil
}

func resolveToolBinaries() (map[string]string, error) {
	tools := map[string]string{}

	qbePath, err := qbe.QBEBinary()
	if err != nil {
		return nil, fmt.Errorf("bundle qbe: %w", err)
	}
	tools[binaryName("qbe")] = qbePath

	for _, name := range bundledToolNames(runtime.GOOS) {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		tools[filepath.Base(path)] = path
	}
	for _, name := range requiredBundledToolNames(runtime.GOOS) {
		if _, ok := tools[binaryName(name)]; !ok {
			return nil, fmt.Errorf("bundle toolchain: %s is required in PATH for %s release packaging", name, runtime.GOOS)
		}
	}
	if runtime.GOOS == "windows" {
		clangPath := tools[binaryName("clang")]
		root64, root32 := windowsMingwRoots(clangPath)
		if root64 == "" || root32 == "" {
			return nil, fmt.Errorf("bundle toolchain: windows clang must come from an MSYS2 mingw64 root with sibling mingw32 (got %s)", clangPath)
		}
	}
	return tools, nil
}

func bundledToolNames(goos string) []string {
	names := []string{"clang", "clang++", "ld.lld", "lld", "lld-link"}
	if goos == "darwin" {
		names = append(names, "ld64.lld")
	}
	return names
}

func requiredBundledToolNames(goos string) []string {
	names := []string{"clang"}
	if goos == "darwin" {
		names = append(names, "ld64.lld")
	}
	return names
}

func copyClangResources(clangPath, libDir string) error {
	out, err := exec.Command(clangPath, "-print-resource-dir").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundle clang resources: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	resourceDir := filepath.Clean(strings.TrimSpace(string(out)))
	if !isDir(resourceDir) {
		return fmt.Errorf("bundle clang resources: invalid resource dir %s", resourceDir)
	}
	return copyDirContents(resourceDir, filepath.Join(libDir, "clang", filepath.Base(resourceDir)))
}

func copyPlatformToolchainRuntime(bundleDir, libDir string, binaries []string, clangPath string) error {
	switch runtime.GOOS {
	case "windows":
		return copyWindowsToolchain(bundleDir, binaries, clangPath)
	case "darwin":
		return copyDarwinSharedLibraries(bundleDir, libDir, binaries)
	default:
		return copySharedLibraries(libDir, binaries, dependenciesFromLdd)
	}
}

func copyWindowsToolchain(bundleDir string, binaries []string, clangPath string) error {
	if err := copyWindowsToolchainDLLs(filepath.Join(bundleDir, "bin"), binaries); err != nil {
		return err
	}

	root64, root32 := windowsMingwRoots(clangPath)
	if err := copyWindowsTargetRoot(root64, filepath.Join(bundleDir, "lib"), filepath.Join(bundleDir, "include")); err != nil {
		return err
	}
	if root32 != "" {
		if err := copyWindowsTargetRoot(root32, filepath.Join(bundleDir, "lib32"), filepath.Join(bundleDir, "include32")); err != nil {
			return err
		}
	}
	return nil
}

func copyWindowsTargetRoot(root string, libDst string, includeDst string) error {
	if root == "" {
		return fmt.Errorf("bundle windows toolchain: empty target root")
	}
	libSrc := filepath.Join(root, "lib")
	if !isDir(libSrc) {
		return fmt.Errorf("bundle windows toolchain: missing lib dir %s", libSrc)
	}
	if err := copyDirContentsFiltered(libSrc, libDst, shouldSkipWindowsToolchainPath); err != nil {
		return fmt.Errorf("bundle windows toolchain libs from %s: %w", libSrc, err)
	}

	includeSrc := filepath.Join(root, "include")
	if isDir(includeSrc) {
		if err := copyDirContentsFiltered(includeSrc, includeDst, shouldSkipWindowsToolchainPath); err != nil {
			return fmt.Errorf("bundle windows toolchain includes from %s: %w", includeSrc, err)
		}
	}
	return nil
}

func shouldSkipWindowsToolchainPath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 {
		return false
	}

	for _, part := range parts {
		lower := strings.ToLower(part)
		switch {
		case strings.HasPrefix(lower, "python"):
			return true
		case strings.HasPrefix(lower, "tcl"):
			return true
		case strings.HasPrefix(lower, "tk"):
			return true
		case lower == "cmake", lower == "pkgconfig", lower == "terminfo":
			return true
		case lower == "clang", lower == "clang-c", lower == "lld":
			return true
		}
	}

	if len(parts) >= 3 && parts[0] == "gcc" {
		for _, part := range parts[3:] {
			switch strings.ToLower(part) {
			case "plugin", "include", "include-fixed", "install-tools",
				"cc1.exe", "cc1plus.exe", "collect2.exe", "lto1.exe", "lto-wrapper.exe", "g++-mapper-server.exe":
				return true
			}
		}
	}
	return false
}

func copyLinux32ToolchainSupport(clangPath, bundleDir string) error {
	startFilePath, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=Scrt1.o")
	if err != nil {
		return fmt.Errorf("bundle toolchain 32-bit support: resolve start files: %w", err)
	}
	if err := copyDirContents(filepath.Dir(startFilePath), filepath.Join(bundleDir, "lib32")); err != nil {
		return fmt.Errorf("bundle toolchain 32-bit support: copy start-file dir %s: %w", filepath.Dir(startFilePath), err)
	}

	libgccPath, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=libgcc.a")
	if err != nil {
		return fmt.Errorf("bundle toolchain 32-bit support: resolve libgcc dir: %w", err)
	}
	if err := copyDirContents(filepath.Dir(libgccPath), filepath.Join(bundleDir, "lib32")); err != nil {
		return fmt.Errorf("bundle toolchain 32-bit support: copy libgcc dir %s: %w", filepath.Dir(libgccPath), err)
	}

	stubs32Path, err := linuxStubs32Header(clangPath)
	if err != nil {
		return err
	}
	if err := copyDirContents(filepath.Dir(stubs32Path), filepath.Join(bundleDir, "include", "gnu")); err != nil {
		return fmt.Errorf("bundle toolchain 32-bit support: copy include dir %s: %w", filepath.Dir(stubs32Path), err)
	}
	return nil
}

func windowsMingwRoots(clangPath string) (string, string) {
	if runtime.GOOS != "windows" || clangPath == "" {
		return "", ""
	}
	root64 := filepath.Clean(filepath.Join(filepath.Dir(clangPath), ".."))
	if !isDir(filepath.Join(root64, "lib")) {
		return "", ""
	}
	root32 := filepath.Clean(filepath.Join(root64, "..", "mingw32"))
	if !isDir(root32) {
		root32 = ""
	}
	return root64, root32
}

func copySharedLibraries(dstDir string, binaries []string, depsFn func(string) ([]string, error)) error {
	deps := make(map[string]struct{})
	for _, bin := range binaries {
		libs, err := depsFn(bin)
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
		if err := copyFile(src, filepath.Join(dstDir, filepath.Base(src))); err != nil {
			return fmt.Errorf("copy toolchain shared lib %s: %w", src, err)
		}
	}
	return nil
}

func copyDarwinSharedLibraries(bundleDir, dstDir string, binaries []string) error {
	paths, err := collectDarwinDependencies(binaries)
	if err != nil {
		return err
	}
	copied := make(map[string]string, len(paths))
	for _, src := range paths {
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy toolchain shared lib %s: %w", src, err)
		}
		copied[src] = dst
	}
	if err := rewriteDarwinBundleLoadCommands(bundleDir, binaries, copied); err != nil {
		return err
	}
	return nil
}

type darwinLoadCommandChange struct {
	Old string
	New string
}

func rewriteDarwinBundleLoadCommands(bundleDir string, binaries []string, copied map[string]string) error {
	binDir := filepath.Join(bundleDir, "bin")

	for _, src := range binaries {
		dst := filepath.Join(binDir, filepath.Base(src))
		args, err := darwinInstallNameChanges(src, src, copied, true)
		if err != nil {
			return err
		}
		if err := applyDarwinInstallNameChanges(dst, args); err != nil {
			return err
		}
	}

	for src, dst := range copied {
		args, err := darwinInstallNameChanges(src, src, copied, false)
		if err != nil {
			return err
		}
		if err := applyDarwinInstallNameChanges(dst, args); err != nil {
			return err
		}
	}
	if err := adHocSignDarwinBundle(bundleDir, binaries, copied); err != nil {
		return err
	}
	return nil
}

func darwinInstallNameChanges(loaderPath, execPath string, copied map[string]string, binary bool) ([]string, error) {
	rawDeps, err := darwinDependencyRefs(loaderPath)
	if err != nil {
		return nil, err
	}
	rpaths, err := darwinRPaths(loaderPath)
	if err != nil {
		return nil, err
	}
	changes, err := darwinBundledDependencyChanges(loaderPath, execPath, rawDeps, rpaths, copied, binary)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(changes)*3+2)
	if !binary {
		args = append(args, "-id", "@rpath/"+filepath.Base(loaderPath))
	}
	for _, change := range changes {
		args = append(args, "-change", change.Old, change.New)
	}
	return args, nil
}

func darwinBundledDependencyChanges(loaderPath, execPath string, rawDeps, rpaths []string, copied map[string]string, binary bool) ([]darwinLoadCommandChange, error) {
	changes := make([]darwinLoadCommandChange, 0)
	for _, raw := range rawDeps {
		resolved, include, err := resolveDarwinDependencyPath(loaderPath, execPath, raw, rpaths)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		dst, ok := copied[resolved]
		if !ok {
			continue
		}
		rewrite := "@loader_path/" + filepath.Base(dst)
		if binary {
			rewrite = "@executable_path/../lib/" + filepath.Base(dst)
		}
		if raw == rewrite {
			continue
		}
		changes = append(changes, darwinLoadCommandChange{
			Old: raw,
			New: rewrite,
		})
	}
	return changes, nil
}

func applyDarwinInstallNameChanges(path string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	cmd := exec.Command("install_name_tool", append(args, path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rewrite darwin load commands for %s: %w\n%s", path, err, out)
	}
	return nil
}

func adHocSignDarwinBundle(bundleDir string, binaries []string, copied map[string]string) error {
	for _, path := range darwinBundleSignTargets(bundleDir, binaries, copied) {
		cmd := exec.Command("codesign", "--force", "--sign", "-", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("codesign darwin artifact %s: %w\n%s", path, err, out)
		}
	}
	return nil
}

func darwinBundleSignTargets(bundleDir string, binaries []string, copied map[string]string) []string {
	targets := make([]string, 0, len(copied)+len(binaries))
	for _, src := range sortedKeys(copied) {
		targets = append(targets, copied[src])
	}
	binDir := filepath.Join(bundleDir, "bin")
	for _, src := range binaries {
		targets = append(targets, filepath.Join(binDir, filepath.Base(src)))
	}
	return targets
}

func copyWindowsToolchainDLLs(binDir string, binaries []string) error {
	for _, dir := range uniqueExistingDirs(toolBinaryDirs(binaries)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read toolchain dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
				continue
			}
			src := filepath.Join(dir, entry.Name())
			if err := copyFile(src, filepath.Join(binDir, entry.Name())); err != nil {
				return fmt.Errorf("copy toolchain dll %s: %w", src, err)
			}
		}
	}
	return nil
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

	for line := range strings.SplitSeq(string(out), "\n") {
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

func dependenciesFromLdd(binary string) ([]string, error) {
	out, err := exec.Command("ldd", binary).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ldd failed: %w\n%s", err, out)
	}
	deps := make([]string, 0)
	seen := map[string]struct{}{}

	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "linux-vdso") {
			continue
		}
		if strings.Contains(line, "=> not found") {
			return nil, fmt.Errorf("missing runtime dependency: %s", line)
		}
		candidate := ""

		if _, after, ok := strings.Cut(line, "=>"); ok {
			fields := strings.Fields(strings.TrimSpace(after))
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
		if _, ok := seen[candidate]; ok || !isFile(candidate) {
			continue
		}
		seen[candidate] = struct{}{}
		deps = append(deps, candidate)
	}
	return deps, nil
}

func collectDarwinDependencies(binaries []string) ([]string, error) {
	type darwinDepScan struct {
		path     string
		execPath string
	}

	queue := make([]darwinDepScan, 0, len(binaries))
	for _, binary := range binaries {
		queue = append(queue, darwinDepScan{path: binary, execPath: binary})
	}

	deps := make([]string, 0)
	seenDeps := map[string]struct{}{}
	scanned := map[string]struct{}{}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		scanKey := current.path + "|" + current.execPath
		if _, ok := scanned[scanKey]; ok {
			continue
		}
		scanned[scanKey] = struct{}{}

		rawDeps, err := darwinDependencyRefs(current.path)
		if err != nil {
			return nil, err
		}
		rpaths, err := darwinRPaths(current.path)
		if err != nil {
			return nil, err
		}

		for _, raw := range rawDeps {
			resolved, include, err := resolveDarwinDependencyPath(current.path, current.execPath, raw, rpaths)
			if err != nil {
				return nil, err
			}
			if !include || resolved == current.path {
				continue
			}
			if _, ok := seenDeps[resolved]; ok {
				continue
			}
			seenDeps[resolved] = struct{}{}
			deps = append(deps, resolved)
			queue = append(queue, darwinDepScan{path: resolved, execPath: current.execPath})
		}
	}

	sort.Strings(deps)
	return deps, nil
}

func darwinDependencyRefs(binary string) ([]string, error) {
	out, err := exec.Command("otool", "-L", binary).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("otool failed: %w\n%s", err, out)
	}
	return parseOtoolDependencies(string(out)), nil
}

func darwinRPaths(binary string) ([]string, error) {
	out, err := exec.Command("otool", "-l", binary).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("otool -l failed: %w\n%s", err, out)
	}
	return parseDarwinRPaths(string(out)), nil
}

func parseOtoolDependencies(out string) []string {
	deps := make([]string, 0)
	for idx, line := range strings.Split(out, "\n") {
		if idx == 0 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		deps = append(deps, fields[0])
	}
	return deps
}

func parseDarwinRPaths(out string) []string {
	rpaths := make([]string, 0)
	inRPath := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "cmd LC_RPATH":
			inRPath = true
		case inRPath && strings.HasPrefix(trimmed, "path "):
			pathText := strings.TrimPrefix(trimmed, "path ")
			if before, _, ok := strings.Cut(pathText, " ("); ok {
				pathText = before
			}
			if pathText != "" {
				rpaths = append(rpaths, pathText)
			}
			inRPath = false
		case strings.HasPrefix(trimmed, "cmd "):
			inRPath = false
		}
	}
	return rpaths
}

func resolveDarwinDependencyPath(loaderPath, execPath, dep string, rpaths []string) (string, bool, error) {
	if dep == "" {
		return "", false, nil
	}
	if strings.HasPrefix(dep, "/usr/lib/") || strings.HasPrefix(dep, "/System/Library/") {
		return "", false, nil
	}
	if strings.HasPrefix(dep, "/") {
		if !isFile(dep) {
			return "", false, fmt.Errorf("missing runtime dependency: %s", dep)
		}
		return dep, true, nil
	}

	loaderDir := filepath.Dir(loaderPath)
	execDir := filepath.Dir(execPath)
	if strings.HasPrefix(dep, "@loader_path/") {
		candidate := filepath.Clean(filepath.Join(loaderDir, strings.TrimPrefix(dep, "@loader_path/")))
		if !isFile(candidate) {
			return "", false, fmt.Errorf("missing runtime dependency: %s referenced by %s", dep, loaderPath)
		}
		return candidate, true, nil
	}
	if strings.HasPrefix(dep, "@executable_path/") {
		candidate := filepath.Clean(filepath.Join(execDir, strings.TrimPrefix(dep, "@executable_path/")))
		if !isFile(candidate) {
			return "", false, fmt.Errorf("missing runtime dependency: %s referenced by %s", dep, loaderPath)
		}
		return candidate, true, nil
	}
	if strings.HasPrefix(dep, "@rpath/") {
		rel := strings.TrimPrefix(dep, "@rpath/")
		for _, rpath := range rpaths {
			expanded := strings.ReplaceAll(rpath, "@loader_path", loaderDir)
			expanded = strings.ReplaceAll(expanded, "@executable_path", execDir)
			candidate := filepath.Clean(filepath.Join(expanded, rel))
			if isFile(candidate) {
				return candidate, true, nil
			}
		}
		return "", false, fmt.Errorf("missing runtime dependency: %s referenced by %s", dep, loaderPath)
	}
	return "", false, nil
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
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || !isFile(path) {
			return err
		}
		return copyFile(path, filepath.Join(dstDir, rel))
	})
}

func copyDirContentsFiltered(srcDir, dstDir string, skip func(string) bool) error {
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return err
		}
		if skip(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !isFile(path) {
			return nil
		}
		return copyFile(path, filepath.Join(dstDir, rel))
	})
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

func toolPaths(tools map[string]string) []string {
	paths := make([]string, 0, len(tools))
	for _, name := range sortedKeys(tools) {
		paths = append(paths, tools[name])
	}
	return paths
}

func toolBinaryDirs(binaries []string) []string {
	dirs := make([]string, 0, len(binaries))
	for _, binary := range binaries {
		dirs = append(dirs, filepath.Clean(filepath.Dir(binary)))
	}
	return dirs
}

func supportsBundled32BitTarget(tools map[string]string) (bool, error) {
	switch runtime.GOOS {
	case "linux":
		if !linux32BitHostArchSupported(runtime.GOARCH) {
			return false, nil
		}
		clangPath, ok := tools[binaryName("clang")]
		if !ok {
			return false, fmt.Errorf("bundle toolchain: clang is required for 32-bit support detection")
		}
		cc, err := resolveRuntimeCompiler(abi.Bits32)
		if err != nil || !supportsLinux32BitRuntimeCompile(cc) {
			return false, nil
		}
		if _, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=Scrt1.o"); err != nil {
			return false, nil
		}
		if _, err := clangResolvedFile(clangPath, "-m32", "-print-file-name=libgcc.a"); err != nil {
			return false, nil
		}
		if _, err := linuxStubs32Header(clangPath); err != nil {
			return false, nil
		}
		return true, nil
	case "windows":
		clangPath, ok := tools[binaryName("clang")]
		if !ok {
			return false, fmt.Errorf("bundle toolchain: clang is required for 32-bit support detection")
		}
		_, root32 := windowsMingwRoots(clangPath)
		return root32 != "", nil
	default:
		return false, nil
	}
}

func linux32BitHostArchSupported(goarch string) bool {
	return goarch == "amd64" || goarch == "386"
}

func supportsLinux32BitRuntimeCompile(compiler string) bool {
	if compiler == "" {
		return false
	}

	tmpDir, err := os.MkdirTemp("", "ferret-m32-probe-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmpDir)

	src := filepath.Join(tmpDir, "probe.c")
	out := filepath.Join(tmpDir, "probe.o")
	probe := "#include <stdint.h>\nint ferret_m32_probe(void) { return (int)sizeof(uintptr_t); }\n"
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		return false
	}

	cmd := exec.Command(compiler, "-m32", "-c", src, "-o", out)
	return cmd.Run() == nil
}

func uniqueExistingDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok || !isDir(clean) {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func binaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
