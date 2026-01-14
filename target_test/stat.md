**Fixed recently**
- Map and array element borrows are now allowed; map/dynamic array index lvalues lower to references (`MapGet`/`ferret_array_get`) so `&mut m["k"]` and `&mut arr[i]` work (including nested `[]T`). `internal/semantics/typechecker/typechecker.go:2298`, `internal/mir/gen/builder.go:3132`, `internal/mir/gen/builder.go:3171`, `internal/codegen/qbe_embeddings/emit.go:747`, `internal/codegen/wasm/emit.go:1441`

**Native: amd64 (Linux/macOS SysV, Windows win64)**
- QBE codegen hard‑fails if MIR ever emits `MakeStruct`, `ExtractField`, `InsertField`, or `MakeArray`. `internal/codegen/qbe_embeddings/qbe.go:271`
Example (conceptual; these MIR ops are not emitted by current lowering):
```ferret
type Point struct { .X: i32, .Y: i32 };

fn main() {
    let p := { .X = 1, .Y = 2 };
    let x := p.X;
    p.Y = 3;
}
```
If those MIR ops were produced, QBE would error; current lowering uses stores/ptr offsets instead.
- Fixed‑size array values are not representable by value (must be byref/pointer); QBE type lowering errors if used as a value. `internal/codegen/qbe_embeddings/emit.go:1840`
Example (compiled via hidden out‑param; by‑value representation is not supported):
```ferret
fn make() -> [3]i32 {
    return [1, 2, 3];
}

fn main() {
    let a := make();
    // [3]i32 is carried by reference internally.
}
```
- Any primitive outside `i8/i16/i32/u8/u16/u32/i64/u64/f32/f64/bool/byte/str` is unsupported by value (large primitives require byref/out‑params). `internal/codegen/qbe_embeddings/emit.go:1812`
Example (large primitives are passed/returned by reference, not by value):
```ferret
fn sum(a: i128, b: i128) -> i128 {
    return a + b;
}
```

**Native: arm64 (Linux/macOS)**
- Same QBE codegen limits as amd64 (above).
- ARM64 backend cannot emit non‑register address forms (assert). `qbe/arm64/emit.c:222`
- ARM64 jump selection has an unhandled case (assert). `qbe/arm64/isel.c:212`
- ABI rejects alignments >16. `qbe/arm64/abi.c:93`
- ARM64 env calls are not supported. `qbe/arm64/abi.c:376`
- Target selection is build‑time; macOS forces amd64 and Windows forces amd64 win64, so no Windows arm64 native output. `internal/codegen/qbe_embeddings/config.h:4`

**WASM**
- `pow` only works for operands convertible to f64 (i32/i64/f32/f64); anything else errors. `internal/codegen/wasm/emit.go:854`
- Unary `-` only supports i32/i64/f32/f64. `internal/codegen/wasm/emit.go:931`
- Casts only supported between wasm value types (i32/i64/f32/f64). `internal/codegen/wasm/emit.go:968`, `internal/codegen/wasm/opcodes.go:265`
- Const/default emission only for wasm value types; other consts error. `internal/codegen/wasm/emit.go:705`, `internal/codegen/wasm/emit.go:798`
- WASM runtime only wires std/io; std/fs, time, random externs aren’t present. `runtime/wasm/runtime.ts:3221`
