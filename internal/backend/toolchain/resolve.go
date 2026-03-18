package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const envToolchainDir = "FERRET_TOOLCHAIN_DIR"

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

func Command(binary string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Env = EnvForBinary(binary, nil)
	return cmd
}

func EnvForBinary(binary string, baseEnv []string) []string {
	env := baseEnv
	if env == nil {
		env = os.Environ()
	}

	binDirs := make([]string, 0, 8)
	if binary != "" {
		binDirs = append(binDirs, filepath.Clean(filepath.Dir(binary)))
	}
	binDirs = append(binDirs, candidateToolchainBinDirs()...)
	binDirs = uniqueExistingDirs(binDirs)

	libDirs := candidateToolchainLibDirs(binary)
	libDirs = uniqueExistingDirs(libDirs)

	out := env
	if len(binDirs) > 0 {
		out = prependPathLikeEnv(out, "PATH", binDirs)
	}
	if len(libDirs) > 0 {
		switch runtime.GOOS {
		case "darwin":
			out = prependPathLikeEnv(out, "DYLD_LIBRARY_PATH", libDirs)
		case "windows":
			out = prependPathLikeEnv(out, "PATH", libDirs)
		default:
			out = prependPathLikeEnv(out, "LD_LIBRARY_PATH", libDirs)
		}
	}

	return out
}

func candidateToolchainBinDirs() []string {
	dirs := make([]string, 0, 16)

	dirs = append(dirs, envToolchainBinDirs()...)
	dirs = append(dirs, autoToolchainBinDirs()...)
	return uniqueDirs(dirs)
}

func candidateToolchainLibDirs(binary string) []string {
	dirs := make([]string, 0, 16)

	if binary != "" {
		binDir := filepath.Clean(filepath.Dir(binary))
		dirs = append(dirs,
			filepath.Join(binDir, "..", "lib"),
			filepath.Join(binDir, "lib"),
		)
	}

	dirs = append(dirs, envToolchainLibDirs()...)
	if binary == "" || isBinaryFromAutoToolchain(binary) {
		dirs = append(dirs, autoToolchainLibDirs()...)
	}
	return uniqueDirs(dirs)
}

func envToolchainBinDirs() []string {
	env := strings.TrimSpace(os.Getenv(envToolchainDir))
	if env == "" {
		return nil
	}
	return []string{
		filepath.Join(env, "bin"),
		env,
	}
}

func envToolchainLibDirs() []string {
	env := strings.TrimSpace(os.Getenv(envToolchainDir))
	if env == "" {
		return nil
	}
	return []string{filepath.Join(env, "lib")}
}

func autoToolchainBinDirs() []string {
	dirs := make([]string, 0, 16)
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		dirs = append(dirs,
			filepath.Join(execDir, "..", "toolchain", "bin"),
			filepath.Join(execDir, "toolchain", "bin"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			dirs = append(dirs, filepath.Join(current, "toolchain", "bin"))
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}
	return dirs
}

func autoToolchainLibDirs() []string {
	dirs := make([]string, 0, 16)
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		dirs = append(dirs,
			filepath.Join(execDir, "..", "toolchain", "lib"),
			filepath.Join(execDir, "toolchain", "lib"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			dirs = append(dirs, filepath.Join(current, "toolchain", "lib"))
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}
	return dirs
}

func isBinaryFromAutoToolchain(binary string) bool {
	if binary == "" {
		return false
	}
	binDir := filepath.Clean(filepath.Dir(binary))
	for _, dir := range uniqueDirs(autoToolchainBinDirs()) {
		if binDir == dir {
			return true
		}
	}
	return false
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

func prependPathLikeEnv(env []string, key string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}

	value := strings.Join(dirs, string(os.PathListSeparator))
	if existing, ok := lookupEnv(env, key); ok && existing != "" {
		value = value + string(os.PathListSeparator) + existing
	}
	return setEnv(env, key, value)
}

func lookupEnv(env []string, key string) (string, bool) {
	for _, pair := range env {
		if !strings.HasPrefix(pair, key+"=") {
			continue
		}
		return strings.TrimPrefix(pair, key+"="), true
	}
	return "", false
}

func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	prefix := key + "="
	replaced := false
	for _, pair := range env {
		if strings.HasPrefix(pair, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, pair)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
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
