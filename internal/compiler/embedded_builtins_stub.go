//go:build !js || !wasm

package compiler

import "compiler/internal/context_v2"

func loadEmbeddedBuiltins(_ *context_v2.CompilerContext) {}
