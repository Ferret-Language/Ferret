package qbe

import (
	"fmt"
	"runtime"

	"compiler/internal/context_v2"
)

// Run invokes the embedded QBE backend to compile SSA into assembly.
func Run(ctx *context_v2.CompilerContext, inputPath, outputPath string) error {
	if inputPath == "" {
		return fmt.Errorf("qbe: missing input path")
	}
	if outputPath == "" {
		return fmt.Errorf("qbe: missing output path")
	}

	args := []string{"qbe"}
	// Set explicit target based on OS and architecture
	// QBE targets: amd64_sysv, amd64_apple, amd64_win64, arm64, arm64_apple, rv64
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "amd64" {
			args = append(args, "-t", "amd64_win64")
		} else {
			return fmt.Errorf("qbe: windows target requires amd64")
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if runtime.GOARCH == "riscv64" {
			args = append(args, "-t", "rv64")
		} else if runtime.GOARCH == "arm64" {
			args = append(args, "-t", "arm64")
		} else {
			args = append(args, "-t", "amd64_sysv")
		}
	case "darwin":
		if runtime.GOARCH == "arm64" {
			args = append(args, "-t", "arm64_apple") // Apple Silicon
		} else {
			args = append(args, "-t", "amd64_apple") // Intel Mac
		}
	}
	args = append(args, "-o", outputPath, inputPath)
	code, err := runQBE(args)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("qbe failed with exit code %d", code)
	}

	return nil
}
