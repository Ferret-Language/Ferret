package qbe

import (
	"compiler/internal/ir/mir"
	"fmt"
	"strings"
)

// MainWrapper emits a QBE IL entry wrapper for non-"main" entry modules:
//
//	export function [rettype] $main() {
//	@start
//	    [%r =rettype] call $<prefix>__main()
//	    ret [%r]
//	}
//
// When the entry module is named "main" the wrapper is not needed because
// qbeSymbol already emits the function as the exported $main symbol directly.
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
	// Entry module named "main": $main is already exported directly.
	if prefix == "main" {
		return "", nil
	}

	symbol := prefix + "__main"
	retType := FunctionReturnQBEType(mainFunc)

	var b strings.Builder
	fmt.Fprintf(&b, "# entry wrapper\n")
	if retType != "" {
		fmt.Fprintf(&b, "export function %s $main() {\n", retType)
	} else {
		fmt.Fprintf(&b, "export function w $main() {\n")
	}
	fmt.Fprintf(&b, "@start\n")
	if retType != "" {
		fmt.Fprintf(&b, "\t%%r =%s call $%s()\n", retType, symbol)
		fmt.Fprintf(&b, "\tret %%r\n")
	} else {
		fmt.Fprintf(&b, "\tcall $%s()\n", symbol)
		fmt.Fprintf(&b, "\tret 0\n")
	}
	fmt.Fprintf(&b, "}\n")
	return b.String(), nil
}
