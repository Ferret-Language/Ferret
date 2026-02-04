//go:build js && wasm

package main

import (
	"encoding/base64"
	"fmt"
	"syscall/js"

	"compiler/internal/compiler"
)

func main() {
	js.Global().Set("ferretCompile", js.FuncOf(compile))
	js.Global().Set("ferretWasmVersion", compiler.Version)
	println("Ferret WASM compiler ready")
	<-make(chan struct{})
}

func compile(this js.Value, args []js.Value) (result any) {
	// Recover from panics to prevent undefined returns
	defer func() {
		if r := recover(); r != nil {
			// This shouldn't happen, but if it does, return a valid error response
			println("PANIC in compile:", r)
			result = map[string]any{
				"success": false,
				"output":  fmt.Sprintf("Internal compiler error: %v", r),
				"wasm":    "",
			}
		}
	}()

	if len(args) < 2 {
		return map[string]any{
			"success": false,
			"output":  "Invalid arguments: expected (code: string | files: object, debug: bool)",
		}
	}

	firstArg := args[0]
	debug := args[1].Bool()

	var opts *compiler.Options

	// Handle both object (multi-file) and string (single-file) input by converting to Files format
	files := make(map[string]string)

	if firstArg.Type() == js.TypeObject {
		// Multi-file mode: { "main.fer": "...", "utils.fer": "..." }
		// Get all keys from the JS object
		keys := js.Global().Get("Object").Call("keys", firstArg)
		length := keys.Length()

		for i := 0; i < length; i++ {
			key := keys.Index(i).String()
			value := firstArg.Get(key).String()
			files[key] = value
		}
	} else {
		// Single-file mode: convert string to Files format with main.fer
		code := firstArg.String()
		files["main.fer"] = code
	}

	opts = &compiler.Options{
		Files:            files,
		Debug:            debug,
		LogFormat:        compiler.HTML,
		CodegenBackend:   "wasm",
		OutputExecutable: "out.wasm",
	}

	compileResult := compiler.Compile(opts)

	wasm := ""
	if len(compileResult.Artifact) > 0 {
		wasm = base64.StdEncoding.EncodeToString(compileResult.Artifact)
	}

	return map[string]any{
		"success": compileResult.Success,
		"output":  compileResult.Output,
		"wasm":    wasm,
	}
}
