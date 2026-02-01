package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"compiler/colors"
	"compiler/internal/context_v2"
	utilsfs "compiler/internal/utils/fs"
)

// BuildOptions configures how to build the executable
type BuildOptions struct {
	Assembler   string   // Assembler (as)
	AsFlags     []string // Assembler flags
	Linker      string   // Linker (ld)
	LinkFlags   []string // Linker flags
	LinkInputs  []string // Linker inputs before objects (e.g., crt1/crti)
	LinkLibs    []string // Linker libraries after objects
	LinkPost    []string // Linker inputs after libraries (e.g., crtn)
	RuntimePath string   // Path to runtime library directory
	OutputPath  string   // Output executable path
	Debug       bool
	LdDefaults  bool // Whether ld defaults were applied
}

// DefaultBuildOptions returns default build options.
func DefaultBuildOptions() *BuildOptions {
	linkFlags := []string{"-s"}
	assembler := ""
	linker := ""

	toolchainPath := resolveToolchainPath()
	if toolchainPath != "" {
		if assembler == "" {
			candidates := []string{filepath.Join(toolchainPath, "as")}
			if runtime.GOOS == "windows" {
				candidates = append([]string{filepath.Join(toolchainPath, "as.exe")}, candidates...)
			}
			for _, candidate := range candidates {
				if utilsfs.IsValidFile(candidate) {
					assembler = candidate
					break
				}
			}
		}
		if linker == "" {
			candidates := []string{filepath.Join(toolchainPath, "ld")}
			if runtime.GOOS == "windows" {
				candidates = append([]string{filepath.Join(toolchainPath, "ld.exe")}, candidates...)
			}
			for _, candidate := range candidates {
				if utilsfs.IsValidFile(candidate) {
					linker = candidate
					break
				}
			}
		}

		libDir := filepath.Join(toolchainPath, "lib")
		if utilsfs.IsDir(libDir) {
			linkFlags = append(linkFlags, "-L", libDir)
		}
	}

	if assembler == "" {
		assembler = "as"
	}
	if linker == "" {
		linker = "ld"
	}

	if !isLdLinker(linker) {
		switch runtime.GOOS {
		case "linux":
			linkFlags = append(linkFlags, "-no-pie")
		case "darwin":
			linkFlags = append(linkFlags, "-Wl,-no_pie")
		case "openbsd":
			linkFlags = append(linkFlags, "-nopie")
		case "freebsd":
			// FreeBSD defaults are acceptable; no PIE flag needed.
		}
	}

	linkLibs := []string{"-lm"}
	if runtime.GOOS == "windows" {
		toolchainLib := ""
		if toolchainPath != "" {
			candidate := filepath.Join(toolchainPath, "lib")
			if utilsfs.IsDir(candidate) {
				toolchainLib = candidate
			}
		}

		// Add Windows-specific flags
		// -m i386pep: Use PE+ format for 64-bit Windows
		linkFlags = append(linkFlags, "-m", "i386pep")
		linkLibs = []string{
			"--start-group", // enable rescanning
			windowsLibArg(toolchainLib, []string{"libmingw32.a"}, "-lmingw32"),
			windowsLibArg(toolchainLib, []string{"libmingwex.a"}, "-lmingwex"),
			windowsLibArg(toolchainLib, []string{"libmsvcrt.a"}, "-lmsvcrt"),
			windowsLibArg(toolchainLib, []string{"libkernel32.a"}, "-lkernel32"),
			windowsLibArg(toolchainLib, []string{"libuser32.a"}, "-luser32"),
			windowsLibArg(toolchainLib, []string{"libgcc.a"}, "-lgcc"),
			windowsLibArg(toolchainLib, []string{"libgcc_eh.a"}, "-lgcc_eh"),
			windowsLibArg(toolchainLib, []string{"libwinpthread.a", "libwinpthread.dll.a"}, "-lwinpthread"),
			windowsLibArg(toolchainLib, []string{"libm.a", "libm.dll.a"}, "-lm"),
			"--end-group",
		}
	}

	return &BuildOptions{
		Assembler:   assembler,
		Linker:      linker,
		LinkFlags:   linkFlags, // -s: strip symbols
		LinkLibs:    linkLibs,
		RuntimePath: "libs",
		Debug:       false,
	}
}

// BuildExecutable assembles QBE output and links with the runtime library.
func BuildExecutable(ctx *context_v2.CompilerContext, asmFiles []string, opts *BuildOptions) error {
	if opts == nil {
		opts = DefaultBuildOptions()
	}

	// Determine runtime path - use from config if available, otherwise from opts
	runtimePath := opts.RuntimePath
	if runtimePath == "" && ctx.Config.RuntimePath != "" {
		runtimePath = ctx.Config.RuntimePath
	}

	// Make it absolute
	if !filepath.IsAbs(runtimePath) {
		if absRuntime, err := filepath.Abs(runtimePath); err == nil {
			runtimePath = absRuntime
		}
	}

	// Check if runtime library directory exists
	if !utilsfs.IsDir(runtimePath) {
		return fmt.Errorf("runtime library directory not found: %s", runtimePath)
	}

	runtimeLib := filepath.Join(runtimePath, "libferret_runtime.a")
	if !utilsfs.IsValidFile(runtimeLib) {
		return fmt.Errorf("runtime library not found: %s", runtimeLib)
	}

	// Determine output path
	outputPath := opts.OutputPath
	if outputPath == "" {
		return fmt.Errorf("output path must be specified")
	}

	assembler := opts.Assembler
	if assembler == "" {
		assembler = "as"
	}
	if err := ensureToolExists("assembler", assembler); err != nil {
		return err
	}

	objFiles := make([]string, 0, len(asmFiles))
	for _, asmFile := range asmFiles {
		if strings.TrimSpace(asmFile) == "" {
			continue
		}
		objPath := strings.TrimSuffix(asmFile, filepath.Ext(asmFile)) + ".o"
		args := append([]string{}, opts.AsFlags...)
		args = append(args, "-o", objPath, asmFile)

		if ctx.Config.Debug || opts.Debug {
			colors.CYAN.Printf("Assembling: %s %v\n", assembler, args)
		}

		cmd := exec.Command(assembler, args...)
		cmd.Env = toolchainEnv()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("assembly failed: %w", err)
		}
		objFiles = append(objFiles, objPath)
	}

	linker := opts.Linker
	if linker == "" {
		return fmt.Errorf("linker must be specified")
	}
	if err := ensureToolExists("linker", linker); err != nil {
		return err
	}

	if err := applyLdDefaults(opts); err != nil {
		return err
	}

	args := []string{}
	args = append(args, opts.LinkFlags...)
	args = append(args, "-o", outputPath)
	args = append(args, opts.LinkInputs...)
	args = append(args, objFiles...)
	args = append(args, runtimeLib)
	args = append(args, opts.LinkLibs...)
	args = append(args, opts.LinkPost...)

	if ctx.Config.Debug || opts.Debug {
		colors.CYAN.Printf("Linking executable: %s %v\n", linker, args)
	}

	cmd := exec.Command(linker, args...)
	cmd.Env = toolchainEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("linking failed: %w", err)
	}

	if ctx.Config.Debug || opts.Debug {
		colors.GREEN.Printf("  ✓ Built: %s\n", outputPath)
	}

	return nil
}

func isLdLinker(linker string) bool {
	base := filepath.Base(linker)
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base == "ld" || strings.HasPrefix(base, "ld.")
}

func ensureToolExists(kind, tool string) error {
	if tool == "" {
		return fmt.Errorf("%s must be specified", kind)
	}
	if _, err := exec.LookPath(tool); err == nil {
		return nil
	}
	if runtime.GOOS == "darwin" {
		return fmt.Errorf("%s not found: %s; install Xcode Command Line Tools with: xcode-select --install", kind, tool)
	}
	return fmt.Errorf("%s not found: %s", kind, tool)
}

// applyLdDefaults sets up platform-specific linker defaults.
// This function only runs for GNU ld linkers (checked via isLdLinker).
// For other linkers (LLD, Gold, clang's linker), users should configure
// linking manually via toolchain-specific options.
func applyLdDefaults(opts *BuildOptions) error {
	if opts == nil || opts.LdDefaults || !isLdLinker(opts.Linker) {
		return nil
	}
	opts.LdDefaults = true

	switch runtime.GOOS {
	case "linux":
		crt1, crti, crtn := findLinuxCrtObjects()
		if crt1 == "" || crti == "" || crtn == "" {
			return fmt.Errorf("ld: missing C runtime objects (crt1/crti/crtn)")
		}

		dyn := findLinuxDynamicLinker()
		if dyn == "" {
			return fmt.Errorf("ld: dynamic linker not found")
		}

		if !containsArg(opts.LinkFlags, "-dynamic-linker") {
			opts.LinkFlags = append(opts.LinkFlags, "-dynamic-linker", dyn)
		}

		opts.LinkInputs = append(opts.LinkInputs, crt1, crti)
		if !containsArg(opts.LinkLibs, "-lc") {
			opts.LinkLibs = append([]string{"-lc"}, opts.LinkLibs...)
		}
		// Link libgcc for compiler runtime functions (e.g., 128-bit float operations)
		// Try to find static libgcc.a first, then fall back to shared libgcc_s.so.1
		libgccLinked := false
		if tcLib := toolchainLibDir(); tcLib != "" {
			// Check for static libgcc.a in toolchain (though bootstrap doesn't copy it)
			if path := filepath.Join(tcLib, "libgcc.a"); utilsfs.IsValidFile(path) {
				opts.LinkLibs = append(opts.LinkLibs, "-l:libgcc.a")
				libgccLinked = true
			}
		}
		if !libgccLinked {
			// Try to find libgcc.a via gcc
			if cc := resolveCCompiler(); cc != "" {
				if path := gccPrintFile(cc, "libgcc.a"); path != "" {
					// Add the directory to library search path and link
					libDir := filepath.Dir(path)
					if !containsLibDir(opts.LinkFlags, libDir) {
						opts.LinkFlags = append(opts.LinkFlags, "-L", libDir)
					}
					opts.LinkLibs = append(opts.LinkLibs, "-lgcc")
					libgccLinked = true
				}
			}
		}
		if !libgccLinked {
			// Fall back to shared library (bootstrap copies libgcc_s.so.1)
			// Use -l: syntax (GNU ld specific) to link against exact filename
			// This works without needing symlinks and is safe because applyLdDefaults
			// only runs for GNU ld linkers
			if tcLib := toolchainLibDir(); tcLib != "" {
				if path := filepath.Join(tcLib, "libgcc_s.so.1"); utilsfs.IsValidFile(path) {
					opts.LinkLibs = append(opts.LinkLibs, "-l:libgcc_s.so.1")
					libgccLinked = true
				}
			}
		}
		// Note: We don't need a fallback to -lgcc_s since -l:libgcc_s.so.1 works
		// without symlinks. If libgcc_s.so.1 doesn't exist, that's a bootstrap issue.
		opts.LinkPost = append(opts.LinkPost, crtn)
	case "android":
		return fmt.Errorf("android builds are not supported here; use install-termux.sh")
	case "windows":
		// Windows uses PE format - no dynamic linker needed (unlike ELF on Linux).
		// The C runtime is provided by libmsvcrt (linked in DefaultBuildOptions).
		// We don't add -lc because Windows doesn't have a separate libc.
		crt2, crtbegin, crtend := findWindowsCrtObjects()
		if crt2 == "" || crtbegin == "" || crtend == "" {
			return fmt.Errorf("ld: missing C runtime objects (crt2/crtbegin/crtend)")
		}
		opts.LinkInputs = append(opts.LinkInputs, crt2, crtbegin)
		opts.LinkPost = append(opts.LinkPost, crtend)
	}

	return nil
}

func findLinuxCrtObjects() (string, string, string) {
	crt1 := ""
	crti := ""
	crtn := ""

	// First, check toolchain/lib directory (bundled files from bootstrap)
	if tcLib := toolchainLibDir(); tcLib != "" {
		if crt1 == "" {
			if path := filepath.Join(tcLib, "crt1.o"); utilsfs.IsValidFile(path) {
				crt1 = path
			}
		}
		if crti == "" {
			if path := filepath.Join(tcLib, "crti.o"); utilsfs.IsValidFile(path) {
				crti = path
			}
		}
		if crtn == "" {
			if path := filepath.Join(tcLib, "crtn.o"); utilsfs.IsValidFile(path) {
				crtn = path
			}
		}
	}

	// If still missing, use gcc to dynamically find files
	cc := resolveCCompiler()
	if cc != "" {
		if crt1 == "" {
			if path := gccPrintFile(cc, "crt1.o"); path != "" {
				crt1 = path
			}
		}
		if crti == "" {
			if path := gccPrintFile(cc, "crti.o"); path != "" {
				crti = path
			}
		}
		if crtn == "" {
			if path := gccPrintFile(cc, "crtn.o"); path != "" {
				crtn = path
			}
		}
	}

	// Last resort: check hardcoded paths
	dirs := linuxLibDirs()
	if crt1 == "" {
		crt1 = firstExisting(dirs, "crt1.o")
	}
	if crti == "" {
		crti = firstExisting(dirs, "crti.o")
	}
	if crtn == "" {
		crtn = firstExisting(dirs, "crtn.o")
	}

	return crt1, crti, crtn
}

func findLinuxDynamicLinker() string {
	candidates := linuxLoaderCandidates()

	// First, check toolchain/lib directory (bundled files from bootstrap)
	if tcLib := toolchainLibDir(); tcLib != "" {
		for _, name := range candidates {
			path := filepath.Join(tcLib, name)
			if utilsfs.IsValidFile(path) {
				return path
			}
		}
	}

	// Then, use gcc to dynamically find the loader
	cc := resolveCCompiler()
	if cc != "" {
		for _, name := range candidates {
			if path := gccPrintFile(cc, name); path != "" {
				return path
			}
		}
		// Also check gcc library directories
		if dirs := gccLibDirs(cc); len(dirs) > 0 {
			for _, dir := range dirs {
				for _, name := range candidates {
					path := filepath.Join(dir, name)
					if utilsfs.IsValidFile(path) {
						return path
					}
				}
			}
		}
	}

	// Last resort: check hardcoded paths
	dirs := linuxLibDirs()
	for _, dir := range dirs {
		for _, name := range candidates {
			path := filepath.Join(dir, name)
			if utilsfs.IsValidFile(path) {
				return path
			}
		}
	}

	return ""
}

func linuxLibDirs() []string {
	dirs := []string{}
	// Highest priority: toolchain/lib directory (bundled files from bootstrap)
	if tc := toolchainLibDir(); tc != "" {
		dirs = append(dirs, tc)
	}
	// Next: use gcc's library directories if available (more dynamic, works across distros)
	if cc := resolveCCompiler(); cc != "" {
		if gccDirs := gccLibDirs(cc); len(gccDirs) > 0 {
			dirs = append(dirs, gccDirs...)
		}
	}
	// Fallback: common system library directories
	dirs = append(dirs, "/lib", "/usr/lib", "/lib64", "/usr/lib64")
	// Last resort: distro-specific paths (these won't exist on all distros like Arch)
	switch runtime.GOARCH {
	case "amd64":
		dirs = append(dirs, "/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu")
	case "arm64":
		dirs = append(dirs, "/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu")
	case "386":
		dirs = append(dirs, "/lib/i386-linux-gnu", "/usr/lib/i386-linux-gnu")
	}

	// Remove duplicates while preserving order
	seen := make(map[string]struct{}, len(dirs))
	unique := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		unique = append(unique, dir)
	}
	return unique
}

func windowsLibDirs() []string {
	dirs := []string{}
	if tc := toolchainLibDir(); tc != "" {
		dirs = append(dirs, tc)
	}

	// Try to get library directories from gcc
	if cc := resolveCCompiler(); cc != "" {
		dirs = append(dirs, gccLibDirs(cc)...)
	}

	// Fallback paths for common MinGW installations
	fallbacks := []string{
		"C:\\msys64\\mingw64\\lib",
		"C:\\msys64\\mingw32\\lib",
		"C:\\mingw64\\lib",
		"C:\\mingw32\\lib",
		"C:\\MinGW\\lib",
	}
	for _, dir := range fallbacks {
		if utilsfs.IsDir(dir) {
			dirs = append(dirs, dir)
		}
	}

	seen := make(map[string]struct{}, len(dirs))
	unique := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		unique = append(unique, dir)
	}
	return unique
}

func findWindowsCrtObjects() (string, string, string) {
	crt2 := ""
	crtbegin := ""
	crtend := ""

	// First, check toolchain/lib directory (bundled files from bootstrap)
	if tcLib := toolchainLibDir(); tcLib != "" {
		if crt2 == "" {
			if path := filepath.Join(tcLib, "crt2.o"); utilsfs.IsValidFile(path) {
				crt2 = path
			}
		}
		if crtbegin == "" {
			if path := filepath.Join(tcLib, "crtbegin.o"); utilsfs.IsValidFile(path) {
				crtbegin = path
			}
		}
		if crtend == "" {
			if path := filepath.Join(tcLib, "crtend.o"); utilsfs.IsValidFile(path) {
				crtend = path
			}
		}
	}

	// If still missing, use gcc to dynamically find files
	cc := resolveCCompiler()
	if cc != "" {
		if crt2 == "" {
			if path := gccPrintFile(cc, "crt2.o"); path != "" {
				crt2 = path
			}
		}
		if crtbegin == "" {
			if path := gccPrintFile(cc, "crtbegin.o"); path != "" {
				crtbegin = path
			}
		}
		if crtend == "" {
			if path := gccPrintFile(cc, "crtend.o"); path != "" {
				crtend = path
			}
		}
	}

	// Last resort: check toolchain/lib directory via windowsLibDirs
	dirs := windowsLibDirs()
	if crt2 == "" {
		crt2 = firstExisting(dirs, "crt2.o")
	}
	if crtbegin == "" {
		crtbegin = firstExisting(dirs, "crtbegin.o")
	}
	if crtend == "" {
		crtend = firstExisting(dirs, "crtend.o")
	}

	return crt2, crtbegin, crtend
}

func firstExisting(dirs []string, name string) string {
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if utilsfs.IsValidFile(path) {
			return path
		}
	}
	return ""
}

func containsArg(args []string, value string) bool {
	return slices.Contains(args, value)
}

func containsLibDir(flags []string, dir string) bool {
	for i, flag := range flags {
		if flag == "-L" && i+1 < len(flags) && flags[i+1] == dir {
			return true
		}
	}
	return false
}

func resolveToolchainPath() string {
	if execPath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(execPath), "../libs/toolchain")
		if utilsfs.IsDir(candidate) {
			return candidate
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "libs", "toolchain")
		if utilsfs.IsDir(candidate) {
			return candidate
		}
	}

	return ""
}

func toolchainLibDir() string {
	if tc := resolveToolchainPath(); tc != "" {
		libDir := filepath.Join(tc, "lib")
		if utilsfs.IsDir(libDir) {
			return libDir
		}
	}
	return ""
}

func toolchainEnv() []string {
	env := os.Environ()
	libDir := toolchainLibDir()
	if libDir == "" {
		return env
	}

	switch runtime.GOOS {
	case "linux":
		return withPathEnv(env, "LD_LIBRARY_PATH", libDir)
	case "darwin":
		return withPathEnv(env, "DYLD_LIBRARY_PATH", libDir)
	case "windows":
		return withPathEnv(env, "PATH", libDir)
	default:
		return env
	}
}

func withPathEnv(env []string, key, dir string) []string {
	if dir == "" {
		return env
	}
	for i, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			continue
		}
		current := strings.TrimPrefix(kv, key+"=")
		if current == "" {
			env[i] = key + "=" + dir
		} else {
			env[i] = key + "=" + dir + string(os.PathListSeparator) + current
		}
		return env
	}
	return append(env, key+"="+dir)
}

func resolveCCompiler() string {
	candidates := []string{"gcc", "cc", "clang"}
	if runtime.GOOS == "darwin" {
		candidates = []string{"clang", "cc", "gcc"}
	}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func gccPrintFile(cc, name string) string {
	if cc == "" {
		return ""
	}
	out, err := exec.Command(cc, "-print-file-name="+name).CombinedOutput()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" || path == name {
		return ""
	}
	if !utilsfs.IsValidFile(path) {
		return ""
	}
	return path
}

func gccLibDirs(cc string) []string {
	if cc == "" {
		return nil
	}
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
		dirs := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			if chunk == "" {
				continue
			}
			dirs = append(dirs, chunk)
		}
		return dirs
	}
	return nil
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

func windowsLibArg(libDir string, candidates []string, fallback string) string {
	if libDir != "" {
		for _, name := range candidates {
			path := filepath.Join(libDir, name)
			if utilsfs.IsValidFile(path) {
				return "-l:" + name
			}
		}
	}
	return fallback
}
