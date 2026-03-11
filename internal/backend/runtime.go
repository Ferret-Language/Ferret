package backend

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeCFile locates ferret_runtime.c.
//
// Search order:
//  1. Executable-relative: the ferret binary lives in bin/, so the runtime is
//     at <execDir>/../runtime/ferret_runtime.c  (canonical installed layout).
//  2. Walk up from the working directory — covers development checkouts where
//     the repo contains both compiler/ and runtime/.
func RuntimeCFile() (string, error) {
	var candidates []string

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "runtime", "ferret_runtime.c"),
			filepath.Join(execDir, "runtime", "ferret_runtime.c"),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			candidates = append(candidates,
				filepath.Join(current, "runtime", "ferret_runtime.c"),
			)
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if c == "" {
			continue
		}
		clean := filepath.Clean(c)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			return clean, nil
		}
	}
	return "", fmt.Errorf("ferret runtime: ferret_runtime.c not found; looked relative to executable and working directory")
}
