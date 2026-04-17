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

	"compiler/internal/core/abi"
)

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

	if after, ok := strings.CutPrefix(dep, "@loader_path/"); ok {
		candidate := filepath.Clean(filepath.Join(loaderDir, after))
		if !isFile(candidate) {
			return "", false, fmt.Errorf("missing runtime dependency: %s referenced by %s", dep, loaderPath)
		}
		return candidate, true, nil
	}

	if after, ok := strings.CutPrefix(dep, "@executable_path/"); ok {
		candidate := filepath.Clean(filepath.Join(execDir, after))
		if !isFile(candidate) {
			return "", false, fmt.Errorf("missing runtime dependency: %s referenced by %s", dep, loaderPath)
		}
		return candidate, true, nil
	}

	if after, ok := strings.CutPrefix(dep, "@rpath/"); ok {
		rel := after
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
