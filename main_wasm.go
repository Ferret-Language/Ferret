//go:build js && wasm

package main

import (
	"encoding/base64"
	"syscall/js"

	"compiler/internal/compiler"
)

func main() {
	js.Global().Set("ferretCompile", js.FuncOf(compile))
	js.Global().Set("ferretWasmVersion", "0.0.2")
	println("Ferret WASM compiler ready")
	<-make(chan struct{})
}

func compile(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return map[string]any{
			"success": false,
			"output":  "Invalid arguments: expected (code: string | files: object, debug: bool)",
		}
	}

	firstArg := args[0]
	debug := args[1].Bool()

	var opts *compiler.Options

	// Check if first argument is an object (multi-file mode) or string (single-file mode)
	if firstArg.Type() == js.TypeObject {
		// Multi-file mode: { "main.fer": "...", "utils.fer": "..." }
		files := make(map[string]string)

		// Get all keys from the JS object
		keys := js.Global().Get("Object").Call("keys", firstArg)
		length := keys.Length()

		for i := 0; i < length; i++ {
			key := keys.Index(i).String()
			value := firstArg.Get(key).String()
			files[key] = value
		}

		opts = &compiler.Options{
			Files:            files,
			Debug:            debug,
			LogFormat:        compiler.HTML,
			CodegenBackend:   "wasm",
			OutputExecutable: "out.wasm",
		}
	} else {
		// Single-file mode (backward compatibility)
		code := firstArg.String()
		opts = &compiler.Options{
			Code:             code,
			Debug:            debug,
			LogFormat:        compiler.HTML,
			CodegenBackend:   "wasm",
			OutputExecutable: "out.wasm",
		}
	}

	result := compiler.Compile(opts)

	wasm := ""
	if len(result.Artifact) > 0 {
		wasm = base64.StdEncoding.EncodeToString(result.Artifact)
	}

	return map[string]any{
		"success": result.Success,
		"output":  result.Output,
		"wasm":    wasm,
	}
}
