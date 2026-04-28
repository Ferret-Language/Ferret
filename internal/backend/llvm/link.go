package llvm

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	"compiler/internal/backend/toolchain"
	"compiler/internal/core/abi"
	"compiler/internal/ir/mir"
	"compiler/internal/tokens"
)

type CompileOptions struct {
	Debug bool
}

// CompileIR compiles LLVM IR text into a native executable at outputPath.
//
// The pipeline is:
//
//	LLVM IR text  →  clang (JIT via lli, or direct compile)  →  executable
//
// clang handles both compilation and linking, picking up the C runtime
// automatically.
func CompileIR(llvmIR, outputPath string, opts CompileOptions) error {
	abiBits := abi.SizeBits()
	runtimeLib, err := backend.RuntimeStaticLib()
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}

	tmp, err := os.MkdirTemp("", "ferret-build-*")
	if err != nil {
		return fmt.Errorf("compile ir: temp dir: %w", err)
	}
	// If FERRET_KEEP_IR is set, copy the IR there for debugging.
	if keepPath := os.Getenv("FERRET_KEEP_IR"); keepPath != "" {
		_ = os.WriteFile(keepPath, []byte(llvmIR), 0o644)
	} else {
		defer os.RemoveAll(tmp)
	}

	irFile := filepath.Join(tmp, "input.ll")
	if err := os.WriteFile(irFile, []byte(llvmIR), 0o644); err != nil {
		return fmt.Errorf("compile ir: write ir: %w", err)
	}

	// clang compiles + links in one pass.
	// Pass the runtime archive as a positional input so we do not depend on
	// driver-specific -l:filename support.
	args := []string{"-Wno-override-module"}
	if abiBits == abi.Bits32 {
		args = append(args, "-m32")
	}
	if opts.Debug {
		if runtime.GOOS == "windows" {
			args = append(args, "-gcodeview")
		} else {
			args = append(args, "-g")
		}
		args = append(args, "-O0", "-fno-omit-frame-pointer")
	}
	if runtime.GOOS != "windows" {
		args = append(args, "-pthread")
	}
	args = append(args, irFile)
	args = append(args, runtimeLib)
	args = append(args, "-o", outputPath)

	clangPath, err := toolchain.ResolveBundledBinary("clang")
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}
	if bundledArgs := toolchain.ClangDriverArgs(clangPath, abiBits); len(bundledArgs) != 0 {
		args = append(args[:2], append(bundledArgs, args[2:]...)...)
	}

	cmd := toolchain.Command(clangPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile ir: clang: %w\n%s", err, out)
	}

	return nil
}

// FunctionReturnLLVMType returns the LLVM IR type string for the function's
// return type, or "void" for void/unknown.
func FunctionReturnLLVMType(fn *mir.Function) string {
	if fn == nil || fn.Result == nil {
		return "void"
	}
	base, err := llvmBaseType(fn.Result)
	if err != nil {
		return "void"
	}
	return base
}

// FunctionReturnIsScalar reports whether the function returns a scalar (non-aggregate).
func FunctionReturnIsScalar(fn *mir.Function) bool {
	if fn == nil || fn.Result == nil {
		return false
	}
	_, err := llvmBaseType(fn.Result)
	return err == nil
}

// llvmBaseType returns the LLVM IR base type string for a Ferret type.
// Returns an error for aggregate (named struct) types.
func llvmBaseType(typ typeinfo.Type) (string, error) {
	switch base := backend.UnwrapNamed(typ).(type) {
	case *typeinfo.ApproxType:
		return llvmBaseType(base.Inner)
	case *typeinfo.BuiltinType:
		if _, bits, ok := tokens.ParseIntegerBuiltin(base.Name); ok {
			return fmt.Sprintf("i%d", bits), nil
		}
		switch base.Name {
		case "bool":
			return "i8", nil
		case "char":
			return "i32", nil
		case "f32":
			return "float", nil
		case "f64":
			return "double", nil
		case "void":
			return "void", nil
		}
	case *typeinfo.PointerType, *typeinfo.RefType, *typeinfo.RawPtrType, *typeinfo.FuncType, *typeinfo.MapType:
		return "ptr", nil
	case *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return "i32", nil
	case *typeinfo.AtomicType:
		return llvmBaseType(base.Inner)
	case *typeinfo.OptionalType:
		if backend.OptionalUsesNiche(base.Inner) {
			return llvmBaseType(base.Inner)
		}
	}
	return "", fmt.Errorf("unsupported llvm base type %s", typ)
}
