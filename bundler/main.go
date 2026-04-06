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

const windowsMSYS2RootEnv = "MSYS2_ROOT"

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
	if err := bundleCore(root, coreDir); err != nil {
		return err
	}
	if err := bundleToolchain(toolchainDir); err != nil {
		return err
	}

	fmt.Printf("packaged %s and %s\n", coreDir, toolchainDir)
	return nil
}

func bundleCore(root, bundleDir string) error {
	binDir := filepath.Join(bundleDir, "bin")
	libsDir := filepath.Join(bundleDir, "libs")
	for _, dir := range []string{bundleDir, binDir, libsDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	if err := syncFerretLibs(filepath.Join(root, "ferret_libs_dev"), libsDir); err != nil {
		return err
	}
	if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir, 0); err != nil {
		return err
	}
	if supportsBundled32BitTarget() {
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

func bundleToolchain(bundleDir string) error {
	binDir := filepath.Join(bundleDir, "bin")
	libDir := filepath.Join(bundleDir, "lib")
	for _, dir := range []string{bundleDir, binDir, libDir} {
		if err := resetDir(dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}

	tools, err := resolveToolBinaries()
	if err != nil {
		return err
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
	if runtime.GOOS == "linux" {
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

	if runtime.GOOS == "windows" {
		msys2Root := windowsMSYS2Root()
		for _, name := range []string{"clang", "clang++", "ld.lld", "lld", "lld-link"} {
			path := filepath.Join(msys2Root, "mingw64", "bin", binaryName(name))
			if isFile(path) {
				tools[filepath.Base(path)] = path
			}
		}
		clangPath, ok := tools[binaryName("clang")]
		if !ok {
			return nil, fmt.Errorf("bundle toolchain: windows clang not found in %s", filepath.Join(msys2Root, "mingw64", "bin"))
		}
		root64, root32 := windowsMingwRoots(clangPath)
		if root64 == "" || root32 == "" {
			return nil, fmt.Errorf("bundle toolchain: windows clang must come from an MSYS2 root with sibling mingw32 (got %s)", clangPath)
		}
		return tools, nil
	}

	for _, name := range []string{"clang", "clang++", "ld.lld", "lld", "lld-link"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		tools[filepath.Base(path)] = path
	}
	if _, ok := tools[binaryName("clang")]; !ok {
		return nil, fmt.Errorf("bundle toolchain: clang is required in PATH for release packaging")
	}
	return tools, nil
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
		return copySharedLibraries(libDir, binaries, dependenciesFromOtool)
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
		return fmt.Errorf("ferret_libs_dev is not a directory: %s", srcDir)
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

func dependenciesFromOtool(binary string) ([]string, error) {
	out, err := exec.Command("otool", "-L", binary).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("otool failed: %w\n%s", err, out)
	}
	deps := make([]string, 0)
	seen := map[string]struct{}{}
	for idx, line := range strings.Split(string(out), "\n") {
		if idx == 0 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		candidate := fields[0]
		if !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "/usr/lib/") || strings.HasPrefix(candidate, "/System/Library/") {
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

func supportsBundled32BitTarget() bool {
	switch runtime.GOOS {
	case "linux":
		return true
	case "windows":
		root64 := filepath.Join(windowsMSYS2Root(), "mingw64")
		_, root32 := windowsMingwRoots(filepath.Join(root64, "bin", binaryName("clang")))
		return root32 != ""
	default:
		return false
	}
}

func windowsMSYS2Root() string {
	root := strings.TrimSpace(os.Getenv(windowsMSYS2RootEnv))
	if root == "" {
		root = `C:\msys64`
	}
	return filepath.Clean(root)
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
