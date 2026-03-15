package llvm

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	"compiler/internal/backend/toolchain"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/mir"
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
	// Locate the pre-compiled runtime archive before touching the temp dir so
	// a missing archive is reported cleanly before any work is done.
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
	if opts.Debug {
		if runtime.GOOS == "windows" {
			args = append(args, "-gcodeview")
		} else {
			args = append(args, "-g")
		}
		args = append(args, "-O0", "-fno-omit-frame-pointer")
	}
	args = append(args, irFile, runtimeLib, "-o", outputPath)

	clangPath, err := toolchain.ResolveBinary("clang", "cc", "gcc")
	if err != nil {
		return fmt.Errorf("compile ir: %w", err)
	}

	cmd := toolchain.Command(clangPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile ir: clang: %w\n%s", err, out)
	}

	return nil
}

// SanitizePath converts an import path into the module prefix used for LLVM
// symbol names, e.g. "math/vec2" → "math__vec2".
func SanitizePath(path string) string {
	return sanitizePath(path)
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
	switch base := unwrapNamed(typ).(type) {
	case *typeinfo.BuiltinType:
		switch base.Name {
		case "bool", "u8", "i8":
			return "i8", nil
		case "u16", "i16":
			return "i16", nil
		case "u32", "i32", "char":
			return "i32", nil
		case "u64", "i64", "usize", "isize":
			return "i64", nil
		case "f32":
			return "float", nil
		case "f64":
			return "double", nil
		case "void":
			return "void", nil
		}
	case *typeinfo.PointerType:
		return "ptr", nil
	case *typeinfo.OptionalType:
		if optionalUsesNiche(base.Inner) {
			return llvmBaseType(base.Inner)
		}
	}
	return "", fmt.Errorf("unsupported llvm base type %s", typ)
}

func optionalUsesNiche(typ typeinfo.Type) bool {
	switch t := unwrapNamed(typ).(type) {
	case *typeinfo.PointerType:
		return true
	case *typeinfo.BuiltinType:
		switch t.Name {
		case "bool", "char":
			return true
		}
	case *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return true
	}
	return false
}

func unwrapNamed(typ typeinfo.Type) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil && named.Decl != nil {
		switch named.Decl.Type.(type) {
		case *ast.EnumType, *ast.ErrorType:
			return &typeinfo.BuiltinType{Name: "i32"}
		}
	}
	return typ
}
