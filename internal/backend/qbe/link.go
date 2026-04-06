package qbe

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/backend"
	"compiler/internal/backend/toolchain"
	"compiler/internal/core/abi"
)

// CompileIR compiles QBE IL text into a native executable at outputPath.
//
// The pipeline is:
//
//	QBE IL text  →  qbe binary  →  assembly (.s)  →  cc  →  executable
//
// The embedded QBE binary is built and cached on first call via QBEBinary().
// The Ferret runtime is pre-compiled to a static archive and linked with -l.
func CompileIR(qbeIR, outputPath string) error {
	qbeBin, err := QBEBinary()
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}

	// Ensure the runtime static archive is built before we start the
	// temporary directory so a build failure is reported cleanly.
	runtimeLib, err := backend.RuntimeStaticLib()
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}

	tmp, err := os.MkdirTemp("", "ferret-build-*")
	if err != nil {
		return fmt.Errorf("compile ir: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	irFile := filepath.Join(tmp, "input.qbe")
	if err := os.WriteFile(irFile, []byte(qbeIR), 0o644); err != nil {
		return fmt.Errorf("compile ir: write ir: %w", err)
	}

	asmFile := filepath.Join(tmp, "output.s")
	qbeCmd := toolchain.Command(qbeBin, "-o", asmFile, irFile)
	if out, err := qbeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile ir: qbe: %w\n%s", err, out)
	}

	// Link: assembly + runtime archive → executable via the bundled clang
	// driver so packaged toolchain resolution stays consistent across backends.
	linkerPath, err := toolchain.ResolveBundledBinary("clang")
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}
	args := toolchain.ClangDriverArgs(linkerPath, abi.SizeBits())
	args = append(args, asmFile, runtimeLib, "-o", outputPath)
	ccCmd := toolchain.Command(linkerPath, args...)
	if out, err := ccCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile ir: link: %w\n%s", err, out)
	}

	return nil
}
