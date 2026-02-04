//go:build !js || !wasm

package compiler

import (
"compiler/internal/context_v2"
"compiler/internal/stdlib"
)

// LoadEmbeddedBuiltins loads stdlib from the embedded filesystem.
// Exported for use by tests that create contexts directly.
func LoadEmbeddedBuiltins(ctx *context_v2.CompilerContext) {
stdlib.LoadEmbedded(ctx)
}

// loadEmbeddedBuiltins is the internal version.
func loadEmbeddedBuiltins(ctx *context_v2.CompilerContext) {
stdlib.LoadEmbedded(ctx)
}
