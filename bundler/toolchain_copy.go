package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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
