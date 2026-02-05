package fs

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveLibsPath finds the libs directory relative to the ferret binary: bin/ferret -> ../libs
func ResolveLibsPath() string {
	// First try the normal approach for production binaries
	if execPath, err := os.Executable(); err == nil {
		// Skip this approach if we're running a test binary
		if !strings.Contains(execPath, "go-build") && !strings.HasSuffix(execPath, ".test") {
			candidate := filepath.Join(filepath.Dir(execPath), "../libs")
			if IsDir(candidate) {
				return candidate
			}
		}
	}

	// For test scenarios or when the executable approach fails,
	// walk up the directory tree from the current working directory
	// to find the project root (containing go.mod or libs directory)
	if cwd, err := os.Getwd(); err == nil {

		// Try current directory first
		candidate := filepath.Join(cwd, "libs")
		if IsDir(candidate) {
			return candidate
		}

		// Walk up the directory tree to find project root
		currentDir := cwd
		for {
			// Check if this directory contains go.mod (project root indicator)
			if IsValidFile(filepath.Join(currentDir, "go.mod")) {
				candidate := filepath.Join(currentDir, "libs")
				if IsDir(candidate) {
					return candidate
				}
			}

			// Check if this directory contains libs directory
			candidate := filepath.Join(currentDir, "libs")
			if IsDir(candidate) {
				return candidate
			}

			// Move up one directory
			parentDir := filepath.Dir(currentDir)
			if parentDir == currentDir {
				// We've reached the root directory
				break
			}
			currentDir = parentDir
		}
	}

	return ""
}