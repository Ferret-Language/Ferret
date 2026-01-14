**Native: amd64 (Linux/macOS SysV, Windows win64)**
- QBE codegen hard‑fails if MIR ever emits `MakeStruct`, `ExtractField`, `InsertField`, or `MakeArray`. `internal/codegen/qbe_embeddings/qbe.go:271`
- Fixed‑size array *values* are not representable (only pointers), so QBE type lowering errors out. `internal/codegen/qbe_embeddings/emit.go:1822`
- Any primitive outside `i8/i16/i32/u8/u16/u32/i64/u64/f32/f64/bool/byte/str` is “unsupported primitive” by value. `internal/codegen/qbe_embeddings/emit.go:1796`

**Native: arm64 (Linux/macOS)**
- Same QBE codegen limits as amd64 (above).
- ARM64 backend cannot emit non‑register address forms (assert). `qbe/arm64/emit.c:222`
- ARM64 jump selection has an unhandled case (assert). `qbe/arm64/isel.c:212`
- ABI rejects alignments >16. `qbe/arm64/abi.c:94`
- No Windows arm64 backend present (win64 code exists only for amd64). `qbe/amd64/win64.c:1`

**WASM**
- Large primitives aren’t lowered: they’re mapped to `i32`, and const parsing is 32‑bit, so i128/u128/i256/u256/f128/f256 are effectively unsupported. `internal/codegen/wasm/emit.go:2846`, `internal/codegen/wasm/emit.go:2890`
- `pow` only works for operands convertible to f64 (i32/i64/f32/f64); anything else errors. `internal/codegen/wasm/emit.go:889`
- Unary `-` only supports i32/i64/f32/f64. `internal/codegen/wasm/emit.go:942`
- Casts only supported between wasm value types (i32/i64/f32/f64). `internal/codegen/wasm/emit.go:995`, `internal/codegen/wasm/opcodes.go:228`
- Const/default emission only for wasm value types; other consts error. `internal/codegen/wasm/emit.go:791`, `internal/codegen/wasm/emit.go:817`
- WASM runtime only wires std/io; std/fs, time, random externs aren’t present. `runtime/wasm/runtime.ts:3233