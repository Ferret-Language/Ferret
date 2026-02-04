package fs

import (
	"os"
	"path/filepath"
)

// resolveLibsPath finds the libs directory relative to the ferret binary: bin/ferret -> ../libs
func ResolveLibsPath() string {
	if execPath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(execPath), "../libs")
		if IsDir(candidate) {
			return candidate
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "libs")
		if IsDir(candidate) {
			return candidate
		}
	}

	return ""
}