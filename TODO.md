# Ferret Compiler TODO

## Current Stage
- Front-end pipeline reaches MIR (parse/resolve/typecheck/CFG/HIR-lower/MIR).
- Large primitive runtime + MIR/QBE pointer ABI implemented (i128/u128/i256/u256/f128/f256).
- Fixed array indexing requires compile-time constants (negative const indices allowed). Non-const index -> T0028.
- Dynamic arrays still use runtime bounds checks.
- Blank identifier '_' is reserved for for-loop iterators; invalid elsewhere.
- Value receiver mutations warn (W0005) and do not affect callers (copy semantics).
- QBE Win64 ABI + emitter available; embedded QBE selects amd64_win64 on Windows.


## Next Work Items
### Incremental plan (easy -> hard, dependency-ordered)
- [x] Symbol docs + externs via comments
  - [x] Lexer emits comment tokens (line/block) and parser groups leading comments (Go-style) into doc blocks.
  - [x] Attach doc blocks only to symbol declarations (let/const/type/fn/method); discard unused comments.
  - [x] Skip non-doc comments during parsing so inline comments don't break expressions.
  - [x] Restrict `@extern` usage to standard library (ferret_libs) and use doc blocks to mark extern symbols.
- [x] Standardize ferret_libs
  - [x] Add `ferret_libs/global.fer` prelude with builtin declarations (e.g., len/append) using `@extern`.
  - [x] Ensure global prelude loads without import and is visible to LSP/symbols.
  - [x] Warn on stdlib `@extern` declarations without runtime implementations.
  - [x] Implement `io::Read` runtime helper and align `ReadInt/ReadFloat` with result out-params.
  - [x] Audit stdlib declarations for extern/implementation consistency.
  - [x] Lower string concatenation to runtime helper in the QBE path.
- [x] Builtin primitive coverage (foundation)
  - [x] Ensure full builtin type support in MIR + QBE (i8..i256/u8..u256, f32..f256, bool, str, byte).
  - [x] Show .0 in floating point numbers
  - [x] Validate const/load/store coverage for those primitives.
  - [x] Limb-based integer runtime (i128/u128/i256/u256) with 32/64-bit limb selection.
- [x] Control flow completeness (depends on large primitive support)
  - [x] MIR lowering for if/else (non-block else, else-if chains).
  - [x] Ensure for/while desugar to supported MIR (range/array for first).
- [x] Structs + enums end-to-end (depends on large primitive support)
  - [x] Solidify data layout for structs/enums and pointer/reference handling.
  - [x] MIR lowering for struct literals/field access/assignment.
  - [x] MIR lowering for enum variants (const values).
  - [x] MIR lowering for enum pattern usage (match on enum variants).
- [x] Methods on named types + cross-module usability (depends on structs/enums)
  - [x] Receiver lowering + name mangling across modules.
  - [x] Cross-module symbol resolution in MIR/QBE (imports + qualified names).
- [x] Collections and loops over them (depends on structs/enums)
  - [x] Builtin `len()` for arrays/maps and `append()` for dynamic arrays.
  - [x] MIR lowering for map literals.
  - [x] MIR lowering for map iteration (MapIterInit/MapIterNext) and map for-loops.
  - [x] MIR lowering for dynamic array literals in expressions.
  - [x] QBE support for map indexing/assignment (MapGet/MapSet + optionals).
- [x] Interfaces (depends on structs/enums + methods)
  - [x] Interface representation (vtable layout + method dispatch).
- [x] References + borrowing (design + implementation)
  - [x] Add unary `&` expression parsing and HIR/MIR lowering (address-of).
  - [x] Enforce borrowable lvalues only; disallow `&&T` and `&` on temporaries.
  - [x] Reference binding rules: `&T` must be initialized with a reference; no rebinding after init.
  - [x] Reference parameter passing requires `&` (no implicit borrow).
  - [x] Reference return ABI: hidden out-param for `-> &T`, return pointer (move-out vs borrow).
  - [x] Write-through assignment for `&T` variables and compound ops.
  - [ ] Decide/refine reference semantics for map and dynamic array elements.
- [x] Functions beyond named decls (hard; depends on structs/enums + methods)
  - [x] Function literals and closure capture strategy (if supported in AST).
- [x] Error flow (depends on large primitive support)
  - [x] MIR lowering for result/catch paths.
- [x] Backend targets
  - [x] Win64 ABI for QBE (amd64_win64 target + emitter + embedding).
  - [x] WASM
  - [x] Decide runtime behavior for dynamic array OOB (panic via ferret_global_panic).
  - [x] Add non-PIE build flags for QBE outputs (Linux/macOS/OpenBSD).

### WASM backend gaps (priority order)
- [x] Codegen: struct ops (MakeStruct / ExtractField / InsertField) to match QBE layout.
- [x] Codegen: array value construction (MakeArray) for fixed arrays and value-level literals.
- [ ] Codegen: map ops (MapGet / MapSet + iteration helpers) with required runtime imports.
- [ ] Codegen: optional/result/union ops (Optional* / Result* / Union*) needed for catch paths.
- [ ] Codegen: call_indirect for interface/closure dispatch.
- [x] Runtime: string concat helpers (ferret_io_ConcatStrings + ferret_string_concat_*).
- [ ] Runtime: large primitive helpers (ferret_{i128,u128,i256,u256,f128,f256}_*).
- [ ] Runtime: map helpers (ferret_map_* + typed constructors + optional get/iter).
- [ ] Stdlib externs in WASM: std/fs, time, random.
