package backend

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeStaticLib locates the pre-compiled ferret_runtime.a static archive.
//
// The archive is produced by build.sh and placed in the libs/ directory that
// sits alongside the bin/ directory containing the ferret executable:
//
//	<root>/
//	  bin/ferret
//	  libs/ferret_runtime.a
//
// Search order:
//  1. Executable-relative: <execDir>/../libs/ferret_runtime.a
//  2. Walk up from the working directory looking for libs/ferret_runtime.a
//     (covers running the compiler directly from a development checkout).
func RuntimeStaticLib() (string, error) {
	var candidates []string

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "libs", "ferret_runtime.a"),
			filepath.Join(execDir, "libs", "ferret_runtime.a"),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			candidates = append(candidates,
				filepath.Join(current, "libs", "ferret_runtime.a"),
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
	return "", fmt.Errorf(
		"ferret runtime: ferret_runtime.a not found; " +
			"run build.sh to compile the runtime before linking",
	)
}
