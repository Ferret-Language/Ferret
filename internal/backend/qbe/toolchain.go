package qbe

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"compiler/internal/backend/toolchain"
)

var (
	qbeBinaryOnce sync.Once
	qbeBinaryPath string
	qbeBinaryErr  error
)

// QBEBinary returns the path to the compiled QBE binary, building and caching
// it from the embedded source on first call.
func QBEBinary() (string, error) {
	qbeBinaryOnce.Do(func() {
		qbeBinaryPath, qbeBinaryErr = ensureQBEBinary()
	})
	return qbeBinaryPath, qbeBinaryErr
}

func ensureQBEBinary() (string, error) {
	if bundled, err := toolchain.ResolveBinary("qbe"); err == nil {
		if info, statErr := os.Stat(bundled); statErr == nil && !info.IsDir() && info.Size() > 0 {
			return bundled, nil
		}
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("qbe toolchain: cannot determine cache dir: %w", err)
	}
	qbeDir := filepath.Join(cacheDir, "ferret", "qbe-"+VendoredCommit[:12])
	binPath := filepath.Join(qbeDir, qbeBinaryName())

	// Already built — fast path.
	if info, err := os.Stat(binPath); err == nil && info.Size() > 0 {
		return binPath, nil
	}

	srcDir := filepath.Join(qbeDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return "", fmt.Errorf("qbe toolchain: mkdir: %w", err)
	}

	if err := extractFS(SourceFS(), srcDir); err != nil {
		return "", fmt.Errorf("qbe toolchain: extract source: %w", err)
	}

	// Generate config.h which sets the default target architecture.
	if err := writeConfigH(srcDir); err != nil {
		return "", fmt.Errorf("qbe toolchain: config.h: %w", err)
	}

	srcs, err := gatherCSources(srcDir, "")
	if err != nil {
		return "", fmt.Errorf("qbe toolchain: gather sources: %w", err)
	}

	ccPath, err := toolchain.ResolveBinary("cc", "clang", "gcc")
	if err != nil {
		return "", fmt.Errorf("qbe toolchain: %w", err)
	}
	args := append([]string{"-o", binPath}, srcs...)
	cmd := toolchain.Command(ccPath, args...)
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qbe toolchain: build failed: %w\n%s", err, out)
	}

	return binPath, nil
}

func qbeBinaryName() string {
	if runtime.GOOS == "windows" {
		return "qbe.exe"
	}
	return "qbe"
}

// deftgt returns the QBE Deftgt symbol for the current OS/arch combination,
// mirroring the logic in the QBE Makefile config.h rule.
func deftgt() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "T_arm64_apple"
		}
		return "T_amd64_apple"
	default: // linux, freebsd, etc.
		switch runtime.GOARCH {
		case "arm64":
			return "T_arm64"
		case "riscv64":
			return "T_rv64"
		default:
			return "T_amd64_sysv"
		}
	}
}

func writeConfigH(srcDir string) error {
	content := fmt.Sprintf("#define Deftgt %s\n", deftgt())
	return os.WriteFile(filepath.Join(srcDir, "config.h"), []byte(content), 0o644)
}

func gatherCSources(srcDir, _ string) ([]string, error) {
	var srcs []string

	// Root-level .c files.
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("read src dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".c" {
			srcs = append(srcs, filepath.Join(srcDir, e.Name()))
		}
	}

	// All arch subdirs — QBE links every backend regardless of host arch.
	for _, sub := range []string{"amd64", "arm64", "rv64"} {
		archDir := filepath.Join(srcDir, sub)
		archEntries, err := os.ReadDir(archDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read arch dir %s: %w", sub, err)
		}
		for _, e := range archEntries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".c" {
				srcs = append(srcs, filepath.Join(archDir, e.Name()))
			}
		}
	}

	return srcs, nil
}

func extractFS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Don't overwrite existing files (idempotent extraction).
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		return copyFSFile(src, path, target)
	})
}

func copyFSFile(src fs.FS, srcPath, dstPath string) error {
	in, err := src.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
