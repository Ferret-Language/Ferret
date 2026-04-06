package backend

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/abi"
)

func RuntimeStaticLibName(bits int) string {
	if bits == 0 {
		bits = abi.SizeBits()
	}
	if bits == abi.Bits32 {
		return "ferret_runtime32.a"
	}
	return "ferret_runtime.a"
}

// RuntimeStaticLib locates the pre-compiled runtime static archive for the
// active target ABI.
//
// The archive is produced by build.sh and placed in the libs/ directory that
// sits alongside the bin/ directory containing the ferret executable:
//
//	<root>/
//	  bin/ferret
//	  libs/ferret_runtime.a
//	  libs/ferret_runtime32.a
//
// Search order:
//  1. Executable-relative: <execDir>/../libs/<runtime archive>
//  2. Walk up from the working directory looking for libs/<runtime archive>
//     (covers running the compiler directly from a development checkout).
func RuntimeStaticLib() (string, error) {
	libName := RuntimeStaticLibName(0)
	var candidates []string

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "libs", libName),
			filepath.Join(execDir, "libs", libName),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		for current := wd; ; current = filepath.Dir(current) {
			candidates = append(candidates,
				filepath.Join(current, "libs", libName),
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
		"ferret runtime: %s not found; run build.sh to compile the runtime before linking",
		libName,
	)
}
