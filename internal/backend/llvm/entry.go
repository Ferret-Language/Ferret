package llvm

import (
	"compiler/internal/middleend/mir"
	"fmt"
	"strings"
)

// MainWrapper emits an LLVM IR entry wrapper for non-"main" entry modules:
//
//	define [rettype|i32] @main() {
//	entry:
//	  [%r = call rettype @prefix__main()]
//	  ret i32 [%r | 0]
//	}
//
// When the entry module is named "main" the wrapper is not needed because
// llvmSymbol already emits the function as @main directly.
func MainWrapper(mod *mir.Module) (string, error) {
	if mod == nil {
		return "", fmt.Errorf("entry wrapper: nil MIR module")
	}

	var mainFunc *mir.Function
	for _, fn := range mod.Functions {
		if fn != nil && fn.Name == "main" {
			mainFunc = fn
			break
		}
	}
	if mainFunc == nil {
		return "", fmt.Errorf("entry wrapper: module %q has no 'main' function", mod.ImportPath)
	}

	prefix := SanitizePath(mod.ImportPath)
	// Entry module named "main": @main is already emitted directly.
	if prefix == "main" {
		return "", nil
	}

	symbol := prefix + "__main"
	retType := FunctionReturnLLVMType(mainFunc)
	isScalar := FunctionReturnIsScalar(mainFunc)

	var b strings.Builder
	fmt.Fprintf(&b, "; entry wrapper\n")
	if retType != "" && retType != "void" && isScalar {
		fmt.Fprintf(&b, "define %s @main() {\n", retType)
	} else {
		fmt.Fprintf(&b, "define i32 @main() {\n")
	}
	fmt.Fprintf(&b, "entry:\n")
	if retType != "" && retType != "void" && isScalar {
		fmt.Fprintf(&b, "  %%r = call %s @%s()\n", retType, symbol)
		if retType == "i32" {
			fmt.Fprintf(&b, "  ret i32 %%r\n")
		} else {
			fmt.Fprintf(&b, "  %%r32 = sext %s %%r to i32\n", retType)
			fmt.Fprintf(&b, "  ret i32 %%r32\n")
		}
	} else {
		fmt.Fprintf(&b, "  call void @%s()\n", symbol)
		fmt.Fprintf(&b, "  ret i32 0\n")
	}
	fmt.Fprintf(&b, "}\n")
	return b.String(), nil
}
