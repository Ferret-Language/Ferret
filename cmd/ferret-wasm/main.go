package main

import (
	"encoding/json"
	"syscall/js"

	compiler "compiler/internal/driver"
)

type compileResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Wasm    string `json:"wasm,omitempty"`
}

func main() {
	js.Global().Set("ferretWasmVersion", js.ValueOf(compiler.CompilerVersion+"-bridge"))
	js.Global().Set("ferretCompile", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return toJSResult(compileResult{
				Success: false,
				Output:  "no source files provided",
			})
		}

		files := map[string]string{}
		first := args[0]
		switch first.Type() {
		case js.TypeString:
			files["main.fer"] = first.String()
		case js.TypeObject:
			keys := js.Global().Get("Object").Call("keys", first)
			for i := 0; i < keys.Length(); i++ {
				k := keys.Index(i).String()
				files[k] = first.Get(k).String()
			}
		default:
			return toJSResult(compileResult{
				Success: false,
				Output:  "invalid compile input; expected source string or file map",
			})
		}

		if len(files) == 0 {
			return toJSResult(compileResult{
				Success: false,
				Output:  "no source files provided",
			})
		}

		if _, ok := files["main.fer"]; !ok {
			if mainFerr, ok2 := files["main.ferr"]; ok2 {
				files["main.fer"] = mainFerr
			}
		}

		if _, ok := files["main.fer"]; !ok {
			return toJSResult(compileResult{
				Success: false,
				Output:  "entry file main.fer (or main.ferr) not found in provided files",
			})
		}

		return toJSResult(compileResult{
			Success: false,
			Output:  "WASM bridge is active, but this compiler branch does not yet emit runnable browser wasm output. Next step is wiring the wasm backend bridge to return result.wasm.",
		})
	}))

	js.Global().Get("console").Call("log", "Ferret WASM bridge ready")
	select {}
}

func toJSResult(res compileResult) any {
	b, err := json.Marshal(res)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}
	}
	obj := js.Global().Get("JSON").Call("parse", string(b))
	return obj
}
