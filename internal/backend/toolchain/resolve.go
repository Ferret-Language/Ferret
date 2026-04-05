package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func ResolveBinary(names ...string) (string, error) {
	normNames := normalizeNames(names)
	if len(normNames) == 0 {
		return "", fmt.Errorf("toolchain: no binary names provided")
	}

	binDirs := candidateToolchainBinDirs()
	tried := make([]string, 0, len(binDirs)*len(normNames)+len(normNames))

	for _, dir := range binDirs {
		for _, name := range normNames {
			candidate := filepath.Join(dir, exeName(name))
			tried = append(tried, candidate)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}

	for _, name := range normNames {
		if resolved, err := exec.LookPath(name); err == nil {
			return resolved, nil
		}
		tried = append(tried, "PATH:"+name)
	}

	return "", fmt.Errorf("toolchain: unable to find any of [%s] (tried %s)", strings.Join(normNames, ", "), strings.Join(tried, ", "))
}

func ResolveBundledBinary(names ...string) (string, error) {
	normNames := normalizeNames(names)
	if len(normNames) == 0 {
		return "", fmt.Errorf("toolchain: no binary names provided")
	}

	binDirs := candidateToolchainBinDirs()
	tried := make([]string, 0, len(binDirs)*len(normNames))
	for _, dir := range binDirs {
		for _, name := range normNames {
			candidate := filepath.Join(dir, exeName(name))
			tried = append(tried, candidate)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("toolchain: unable to find bundled binary [%s] (tried %s)", strings.Join(normNames, ", "), strings.Join(tried, ", "))
}

func Command(binary string, args ...string) *exec.Cmd {
	return exec.Command(binary, args...)
}

func ClangDriverArgs(clangPath string, bits int) []string {
	if clangPath == "" {
		return nil
	}
	binDir := filepath.Clean(filepath.Dir(clangPath))
	root := filepath.Clean(filepath.Join(binDir, ".."))
	if filepath.Base(root) != "toolchain" {
		return nil
	}

	includeDir := filepath.Join(root, "include")
	libDir := filepath.Join(root, "lib")
	if bits == 32 {
		libDir = filepath.Join(root, "lib32")
	}
	linkerFlavor := "lld"
	target := ""
	if runtime.GOOS == "windows" {
		if bits == 32 {
			target = "i686-w64-windows-gnu"
			includeDir = filepath.Join(root, "include32")
		} else {
			target = "x86_64-w64-windows-gnu"
		}
	}
	args := []string{
		"-fuse-ld=" + linkerFlavor,
		"-B" + binDir,
		"-B" + libDir,
		"-L" + libDir,
		"-isystem", includeDir,
	}
	if target != "" {
		args = append([]string{"--target=" + target}, args...)
	}
	return args
}

func candidateToolchainBinDirs() []string {
	dirs := make([]string, 0, 16)
	dirs = append(dirs, autoToolchainBinDirs()...)
	return uniqueDirs(dirs)
}

func autoToolchainBinDirs() []string {
	dirs := make([]string, 0, 4)
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		dirs = append(dirs,
			filepath.Join(execDir, "..", "toolchain", "bin"),
			filepath.Join(execDir, "..", "..", "toolchain", "bin"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "build", "toolchain", "bin"))
	}
	return dirs
}

func autoToolchainLibDirs() []string {
	dirs := make([]string, 0, 6)
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		dirs = append(dirs,
			filepath.Join(execDir, "..", "toolchain", "lib"),
			filepath.Join(execDir, "..", "toolchain", "lib32"),
			filepath.Join(execDir, "..", "..", "toolchain", "lib"),
			filepath.Join(execDir, "..", "..", "toolchain", "lib32"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs,
			filepath.Join(wd, "build", "toolchain", "lib"),
			filepath.Join(wd, "build", "toolchain", "lib32"),
		)
	}
	return dirs
}

func normalizeNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		trimmed = strings.TrimSuffix(trimmed, ".exe")
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func uniqueExistingDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func uniqueDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
