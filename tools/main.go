package main

import (
	"compiler/colors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if err := run(); err != nil {
		colors.RED.Fprintln(os.Stderr, "bootstrap:", err)
		os.Exit(1)
	}

	colors.GREEN.Println("built successfully!")
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	if runtime.GOOS == "android" {
		return fmt.Errorf("android/termux bootstrap is handled by install-termux.sh")
	}

	libsDir := filepath.Join(root, "libs")
	if err := os.MkdirAll(libsDir, 0755); err != nil {
		return fmt.Errorf("create libs dir: %w", err)
	}

	if err := syncFerretLibs(filepath.Join(root, "ferret_libs"), libsDir); err != nil {
		return err
	}

	if err := buildRuntimeLib(filepath.Join(root, "runtime"), libsDir); err != nil {
		return err
	}

	if shouldBundleToolchain() {
		if err := copyToolchain(libsDir); err != nil {
			return err
		}
	} else {
		toolchainDir := filepath.Join(libsDir, "toolchain")
		if err := os.RemoveAll(toolchainDir); err != nil {
			return fmt.Errorf("clean toolchain dir: %w", err)
		}
	}

	return nil
}

func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}

	dir := cwd
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}

	return "", fmt.Errorf("go.mod not found from %s", cwd)
}

func syncFerretLibs(srcDir, destDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("ferret_libs not found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ferret_libs is not a directory: %s", srcDir)
	}

	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		destPath := filepath.Join(destDir, rel)
		if entry.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		if filepath.Ext(path) != ".fer" {
			return nil
		}
		return copyFile(path, destPath)
	})
}

func buildRuntimeLib(runtimeDir, libsDir string) error {
	// Collect C files from both core and libs subdirectories
	coreDir := filepath.Join(runtimeDir, "core")
	libsRuntimeDir := filepath.Join(runtimeDir, "libs")

	coreFiles, err := filepath.Glob(filepath.Join(coreDir, "*.c"))
	if err != nil {
		return fmt.Errorf("scan core runtime sources: %w", err)
	}
	libsFiles, err := filepath.Glob(filepath.Join(libsRuntimeDir, "*.c"))
	if err != nil {
		return fmt.Errorf("scan libs runtime sources: %w", err)
	}

	cFiles := append(coreFiles, libsFiles...)
	if len(cFiles) == 0 {
		return fmt.Errorf("no runtime C files found in %s/core or %s/libs", runtimeDir, runtimeDir)
	}

	cc, err := resolveCCompiler()
	if err != nil {
		return err
	}
	ar, err := resolveCompilerTool(cc, "ar", "ar")
	if err != nil {
		return err
	}

	objDir, err := os.MkdirTemp("", "ferret-rt-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(objDir)

	for _, src := range cFiles {
		obj := filepath.Join(objDir, strings.TrimSuffix(filepath.Base(src), ".c")+".o")
		args := []string{"-std=c99", "-O2", "-w"}
		if runtime.GOOS == "linux" {
			args = append(args, "-fno-pie")
		}
		// Add both core and libs directories to include path so headers can be found
		args = append(args, "-I", coreDir, "-I", libsRuntimeDir, "-c", src, "-o", obj)

		cmd := exec.Command(cc, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("compile %s: %w", filepath.Base(src), err)
		}
	}

	objFiles, err := filepath.Glob(filepath.Join(objDir, "*.o"))
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	if len(objFiles) == 0 {
		return fmt.Errorf("no runtime objects built")
	}

	libPath := filepath.Join(libsDir, "libferret_runtime.a")
	arArgs := append([]string{"rcs", libPath}, objFiles...)
	arCmd := exec.Command(ar, arArgs...)
	arCmd.Stdout = os.Stdout
	arCmd.Stderr = os.Stderr
	if err := arCmd.Run(); err != nil {
		return fmt.Errorf("archive runtime library: %w", err)
	}

	if ranlib, err := exec.LookPath("ranlib"); err == nil {
		ranCmd := exec.Command(ranlib, libPath)
		ranCmd.Stdout = os.Stdout
		ranCmd.Stderr = os.Stderr
		if err := ranCmd.Run(); err != nil {
			return fmt.Errorf("ranlib failed: %w", err)
		}
	}

	return nil
}

func copyToolchain(libsDir string) error {
	toolchainDir := filepath.Join(libsDir, "toolchain")
	if err := os.RemoveAll(toolchainDir); err != nil {
		return fmt.Errorf("clean toolchain dir: %w", err)
	}
	if err := os.MkdirAll(toolchainDir, 0755); err != nil {
		return fmt.Errorf("create toolchain dir: %w", err)
	}
	libDir := filepath.Join(toolchainDir, "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return fmt.Errorf("create toolchain lib dir: %w", err)
	}

	cc, err := resolveCCompiler()
	if err != nil {
		return err
	}
	asPath, err := resolveCompilerTool(cc, "as", "as")
	if err != nil {
		return err
	}
	ldPath, err := resolveCompilerTool(cc, "ld", "ld")
	if err != nil {
		return err
	}

	asDest := toolBinaryName("as", asPath)
	if err := copyFile(asPath, filepath.Join(toolchainDir, asDest)); err != nil {
		return fmt.Errorf("copy as: %w", err)
	}
	ldDest := toolBinaryName("ld", ldPath)
	if err := copyFile(ldPath, filepath.Join(toolchainDir, ldDest)); err != nil {
		return fmt.Errorf("copy ld: %w", err)
	}

	if err := copyToolchainDeps(libDir, cc, asPath, ldPath); err != nil {
		return err
	}

	return nil
}

func resolveToolPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	return path, nil
}

func resolveCCompiler() (string, error) {
	candidates := []string{"gcc", "cc", "clang"}
	if runtime.GOOS == "darwin" {
		candidates = []string{"clang", "cc", "gcc"}
	}
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("C compiler not found (tried: %s)", strings.Join(candidates, ", "))
}

func resolveCompilerTool(cc, tool, fallback string) (string, error) {
	if cc != "" {
		if name := compilerProgName(cc, tool); name != "" {
			if path, err := exec.LookPath(name); err == nil {
				return path, nil
			}
		}
	}
	return resolveToolPath(fallback)
}

func compilerProgName(cc, tool string) string {
	if cc == "" {
		return ""
	}
	out, err := exec.Command(cc, "-print-prog-name="+tool).CombinedOutput()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return ""
	}
	return name
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
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

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func shouldBundleToolchain() bool {
	return runtime.GOOS != "darwin"
}

func copyToolchainDeps(libDir, cc, asPath, ldPath string) error {
	switch runtime.GOOS {
	case "linux":
		return copyLinuxToolchain(libDir, cc, asPath, ldPath)
	case "windows":
		return copyWindowsToolchain(libDir, cc, asPath, ldPath)
	default:
		return copyBinaryDeps(libDir, asPath, ldPath)
	}
}

func copyLinuxToolchain(libDir, cc, asPath, ldPath string) error {
	if cc == "" {
		return fmt.Errorf("C compiler not found")
	}

	crt1 := gccPrintFile(cc, "crt1.o")
	crti := gccPrintFile(cc, "crti.o")
	crtn := gccPrintFile(cc, "crtn.o")
	libc := gccPrintFile(cc, "libc.so")
	libm := gccPrintFile(cc, "libm.so")
	libgcc := gccPrintFile(cc, "libgcc_s.so.1")
	if libgcc == "" {
		libgcc = gccPrintFile(cc, "libgcc_s.so")
	}

	if err := copyIfExists(libDir, crt1, crti, crtn, libc, libm, libgcc); err != nil {
		return err
	}

	if loader := findLinuxDynamicLoader(cc); loader != "" {
		if err := copyIfExists(libDir, loader); err != nil {
			return err
		}
	}

	if err := copyBinaryDeps(libDir, asPath, ldPath); err != nil {
		return err
	}

	return nil
}

func copyWindowsToolchain(libDir, cc, asPath, ldPath string) error {
	if cc == "" {
		return fmt.Errorf("C compiler not found")
	}
	ccPath, _ := exec.LookPath(cc)
	libDirs := gccLibDirs(cc)

	crt2 := gccPrintFile(cc, "crt2.o")
	if crt2 == "" {
		crt2 = findInDirs(libDirs, "crt2.o")
	}
	crtbegin := gccPrintFile(cc, "crtbegin.o")
	if crtbegin == "" {
		crtbegin = findInDirs(libDirs, "crtbegin.o")
	}
	crtend := gccPrintFile(cc, "crtend.o")
	if crtend == "" {
		crtend = findInDirs(libDirs, "crtend.o")
	}

	libm := gccPrintFile(cc, "libm.a")
	if libm == "" {
		libm = gccPrintFile(cc, "libm.dll.a")
	}
	if libm == "" {
		libm = findInDirs(libDirs, "libm.a")
		if libm == "" {
			libm = findInDirs(libDirs, "libm.dll.a")
		}
	}
	libmsvcrt := gccPrintFile(cc, "libmsvcrt.a")
	if libmsvcrt == "" {
		libmsvcrt = gccPrintFile(cc, "libmsvcrt.dll.a")
	}
	if libmsvcrt == "" {
		libmsvcrt = findInDirs(libDirs, "libmsvcrt.a")
		if libmsvcrt == "" {
			libmsvcrt = findInDirs(libDirs, "libmsvcrt.dll.a")
		}
	}
	libmingw32 := gccPrintFile(cc, "libmingw32.a")
	if libmingw32 == "" {
		libmingw32 = findInDirs(libDirs, "libmingw32.a")
	}
	libmingwex := gccPrintFile(cc, "libmingwex.a")
	if libmingwex == "" {
		libmingwex = findInDirs(libDirs, "libmingwex.a")
	}
	libgcc := gccPrintFile(cc, "libgcc.a")
	if libgcc == "" {
		libgcc = findInDirs(libDirs, "libgcc.a")
	}
	libgcceh := gccPrintFile(cc, "libgcc_eh.a")
	if libgcceh == "" {
		libgcceh = findInDirs(libDirs, "libgcc_eh.a")
	}
	libgccs := gccPrintFile(cc, "libgcc_s.a")
	if libgccs == "" {
		libgccs = gccPrintFile(cc, "libgcc_s.dll.a")
	}
	if libgccs == "" {
		libgccs = findInDirs(libDirs, "libgcc_s.a")
		if libgccs == "" {
			libgccs = findInDirs(libDirs, "libgcc_s.dll.a")
		}
	}
	libwinpthread := gccPrintFile(cc, "libwinpthread.a")
	if libwinpthread == "" {
		libwinpthread = gccPrintFile(cc, "libwinpthread.dll.a")
	}
	if libwinpthread == "" {
		libwinpthread = findInDirs(libDirs, "libwinpthread.a")
		if libwinpthread == "" {
			libwinpthread = findInDirs(libDirs, "libwinpthread.dll.a")
		}
	}
	libkernel32 := gccPrintFile(cc, "libkernel32.a")
	if libkernel32 == "" {
		libkernel32 = findInDirs(libDirs, "libkernel32.a")
	}
	libuser32 := gccPrintFile(cc, "libuser32.a")
	if libuser32 == "" {
		libuser32 = findInDirs(libDirs, "libuser32.a")
	}

	if err := copyIfExists(libDir, crt2, crtbegin, crtend, libm, libmsvcrt, libmingw32, libmingwex, libgcc, libgcceh, libgccs, libwinpthread, libkernel32, libuser32); err != nil {
		return err
	}

	dllNames := []string{
		"libgcc_s_seh-1.dll",
		"libgcc_s_dw2-1.dll",
		"libwinpthread-1.dll",
		"libstdc++-6.dll",
	}
	dllDirs := []string{}
	if ccPath != "" {
		dllDirs = append(dllDirs, filepath.Dir(ccPath))
	}
	dlls := make([]string, 0, len(dllNames))
	for _, name := range dllNames {
		path := gccPrintFile(cc, name)
		if path == "" && len(dllDirs) > 0 {
			path = findInDirs(dllDirs, name)
		}
		dlls = append(dlls, path)
	}
	if err := copyIfExists(libDir, dlls...); err != nil {
		return err
	}

	if err := copyBinaryDeps(libDir, asPath, ldPath); err != nil {
		return err
	}

	return nil
}

func copyBinaryDeps(libDir string, binaries ...string) error {
	switch runtime.GOOS {
	case "linux":
		return copyWithLdd(libDir, binaries...)
	case "darwin":
		return copyWithOtool(libDir, binaries...)
	default:
		return nil
	}
}

func copyWithLdd(libDir string, binaries ...string) error {
	seen := map[string]struct{}{}
	for _, bin := range binaries {
		if bin == "" {
			continue
		}
		paths, err := lddPaths(bin)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			if err := copyIfExists(libDir, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func toolBinaryName(defaultName, path string) string {
	base := filepath.Base(path)
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(base), ".exe") {
		return base
	}
	if runtime.GOOS == "windows" {
		return defaultName + ".exe"
	}
	return defaultName
}

func findInDirs(dirs []string, name string) string {
	if name == "" {
		return ""
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func copyWithOtool(libDir string, binaries ...string) error {
	searchDirs := darwinSearchDirs(binaries...)
	seen := map[string]struct{}{}
	for _, bin := range binaries {
		if bin == "" {
			continue
		}
		paths, err := otoolPaths(bin)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			if strings.HasPrefix(path, "@") {
				resolved := resolveDarwinPath(path, bin, searchDirs)
				if resolved == "" {
					continue
				}
				path = resolved
			}
			if err := copyIfExists(libDir, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func lddPaths(bin string) ([]string, error) {
	out, err := exec.Command("ldd", bin).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ldd %s: %w", filepath.Base(bin), err)
	}
	lines := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "linux-vdso") {
			continue
		}
		if strings.Contains(line, "=>") {
			parts := strings.Split(line, "=>")
			if len(parts) < 2 {
				continue
			}
			right := strings.TrimSpace(parts[1])
			path := strings.Fields(right)
			if len(path) == 0 || path[0] == "not" {
				continue
			}
			paths = append(paths, path[0])
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
			paths = append(paths, fields[0])
		}
	}
	return paths, nil
}

func otoolPaths(bin string) ([]string, error) {
	out, err := exec.Command("otool", "-L", bin).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("otool %s: %w", filepath.Base(bin), err)
	}
	lines := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
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
		path := fields[0]
		paths = append(paths, path)
	}
	return paths, nil
}

func copyIfExists(destDir string, paths ...string) error {
	for _, path := range paths {
		if path == "" || !fileExists(path) {
			continue
		}
		if err := copyFile(path, filepath.Join(destDir, filepath.Base(path))); err != nil {
			return err
		}
	}
	return nil
}

func gccPrintFile(cc, name string) string {
	out, err := exec.Command(cc, "-print-file-name="+name).CombinedOutput()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	if fileExists(path) {
		return path
	}
	if runtime.GOOS == "windows" {
		if path == name || !strings.ContainsAny(path, `/\`) {
			if dirs := gccLibDirs(cc); len(dirs) > 0 {
				if resolved := findInDirs(dirs, name); resolved != "" {
					return resolved
				}
			}
		}
		if ccPath, err := exec.LookPath(cc); err == nil {
			fallback := filepath.Join(filepath.Dir(ccPath), "..", "lib")
			if resolved := findInDirs([]string{fallback}, name); resolved != "" {
				return resolved
			}
		}
	}
	return ""
}

func findLinuxDynamicLoader(cc string) string {
	candidates := linuxLoaderCandidates()
	for _, name := range candidates {
		if path := gccPrintFile(cc, name); path != "" {
			return path
		}
	}

	dirs := gccLibDirs(cc)
	for _, dir := range dirs {
		for _, name := range candidates {
			path := filepath.Join(dir, name)
			if fileExists(path) {
				return path
			}
		}
	}

	return ""
}

func linuxLoaderCandidates() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"ld-linux-x86-64.so.2", "ld-musl-x86_64.so.1"}
	case "arm64":
		return []string{"ld-linux-aarch64.so.1", "ld-musl-aarch64.so.1"}
	case "386":
		return []string{"ld-linux.so.2", "ld-musl-i386.so.1"}
	default:
		return []string{"ld-linux.so.2", "ld-musl.so.1"}
	}
}

func gccLibDirs(cc string) []string {
	out, err := exec.Command(cc, "-print-search-dirs").CombinedOutput()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "libraries:") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		raw := strings.TrimSpace(parts[1])
		if raw == "" {
			continue
		}
		chunks := strings.Split(raw, string(os.PathListSeparator))
		dirs := make([]string, 0, len(chunks)+1)
		msysRoot := ""
		if runtime.GOOS == "windows" {
			msysRoot = windowsMsysRoot(cc)
		}
		for _, chunk := range chunks {
			if chunk == "" {
				continue
			}
			if runtime.GOOS == "windows" {
				chunk = normalizeWindowsGccPath(chunk, msysRoot)
			}
			dirs = append(dirs, chunk)
		}
		if runtime.GOOS == "windows" {
			if libDir := windowsMingwLibDir(cc); libDir != "" {
				dirs = append(dirs, libDir)
			}
		}
		return dirs
	}
	return nil
}

func windowsMsysRoot(cc string) string {
	ccPath, err := exec.LookPath(cc)
	if err != nil || ccPath == "" {
		return ""
	}
	binDir := filepath.Dir(ccPath)
	prefix := filepath.Dir(binDir)
	if !dirExists(prefix) {
		return ""
	}
	root := filepath.Dir(prefix)
	if !dirExists(root) {
		return ""
	}
	return root
}

func windowsMingwLibDir(cc string) string {
	ccPath, err := exec.LookPath(cc)
	if err != nil || ccPath == "" {
		return ""
	}
	binDir := filepath.Dir(ccPath)
	prefix := filepath.Dir(binDir)
	libDir := filepath.Join(prefix, "lib")
	if dirExists(libDir) {
		return libDir
	}
	return ""
}

func normalizeWindowsGccPath(path, msysRoot string) string {
	if path == "" {
		return path
	}
	path = filepath.FromSlash(path)
	if len(path) >= 2 && path[1] == ':' {
		return filepath.Clean(path)
	}
	if msysRoot == "" {
		return filepath.Clean(path)
	}
	if strings.HasPrefix(path, string(os.PathSeparator)) {
		trimmed := strings.TrimPrefix(path, string(os.PathSeparator))
		return filepath.Join(msysRoot, trimmed)
	}
	return filepath.Clean(path)
}

func darwinSearchDirs(binaries ...string) []string {
	dirs := []string{
		"/usr/lib",
		"/usr/local/lib",
		"/Library/Developer/CommandLineTools/usr/lib",
	}

	for _, bin := range binaries {
		if bin == "" {
			continue
		}
		binDir := filepath.Dir(bin)
		dirs = append(dirs, binDir, filepath.Join(binDir, "..", "lib"))
	}

	if sdk := xcrunSDKPath(); sdk != "" {
		dirs = append(dirs, filepath.Join(sdk, "usr", "lib"))
	}
	if clang := xcrunFind("clang"); clang != "" {
		dirs = append(dirs, filepath.Join(filepath.Dir(clang), "..", "lib"))
	}
	if ld := xcrunFind("ld"); ld != "" {
		dirs = append(dirs, filepath.Join(filepath.Dir(ld), "..", "lib"))
	}

	seen := make(map[string]struct{}, len(dirs))
	unique := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		unique = append(unique, dir)
	}

	return unique
}

func resolveDarwinPath(path, bin string, searchDirs []string) string {
	switch {
	case strings.HasPrefix(path, "@loader_path/"):
		rel := strings.TrimPrefix(path, "@loader_path/")
		candidate := filepath.Join(filepath.Dir(bin), rel)
		if fileExists(candidate) {
			return candidate
		}
	case strings.HasPrefix(path, "@executable_path/"):
		rel := strings.TrimPrefix(path, "@executable_path/")
		candidate := filepath.Join(filepath.Dir(bin), rel)
		if fileExists(candidate) {
			return candidate
		}
	case strings.HasPrefix(path, "@rpath/"):
		name := strings.TrimPrefix(path, "@rpath/")
		if resolved := findInDirs(searchDirs, name); resolved != "" {
			return resolved
		}
	}

	if strings.HasPrefix(path, "/") && fileExists(path) {
		return path
	}
	return ""
}

func xcrunFind(tool string) string {
	out, err := exec.Command("xcrun", "--find", tool).CombinedOutput()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" || !fileExists(path) {
		return ""
	}
	return path
}

func xcrunSDKPath() string {
	out, err := exec.Command("xcrun", "--show-sdk-path").CombinedOutput()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" || !fileExists(path) {
		return ""
	}
	return path
}
