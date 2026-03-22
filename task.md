# Ferret Parallel Task Board

This board is split for concurrent subagent work with minimal file overlap.

## Track A: Semantics + Typechecker

- [*] Sync `CURRENT_SEMANTICS.md` with actual parser/typechecker behavior (`&` raw coercion model, no `@`, `mut` param call semantics, raw/owner cast boundary).
- [*] Enforce cast boundary in typechecker: reject explicit `*T <-> ^T` casts even inside `unsafe`.
- [*] Add explicit ownership transfer API design for raw-owner boundary (`adopt` / `expose`) and codify in semantics + std libs.
- [*] Add diagnostics guidance for prohibited owner/raw casts that references the final API name once it lands.

Files:
- `internal/analysis/semantics/typechecker/typechecker.go`
- `internal/analysis/semantics/typechecker/typechecker_test.go`
- `CURRENT_SEMANTICS.md`

## Track B: Allocator Surface

- [*] Add typed allocator helpers in `std/mem` (safe wrappers around raw allocation paths) consistent with current pointer model.
- [*] Resolve blocker: cross-module generic functions with bodies are now specialized across modules before backend lowering.
- [*] Verify arena API semantics stay region-style (`Free` no-op, `Reset` reuse, `Release` final free) and document usage constraints.
- [*] Add/extend smoke tests for typed allocation helpers and arena lifecycle misuse cases.
- [*] Follow-up: investigate runtime crash path for allocator-interface `Realloc(...)` calls (observed in dedicated typed/realloc smoke runs).

Files:
- `ferret_libs_dev/std/mem.ferr`
- `allocator_smoke.ferr`
- `allocator_ffi_smoke.ferr`

## Track C: Language Server + Docs

- [*] Extend hover text to explain raw/owner cast prohibition and suggest ownership API once available.
- [*] Add module-level docs entry about pointer categories and unsafe raw coercion examples.
- [*] Add quick-reference examples for `unsafe { let p: ^T = &x }` and by-value `mut` parameters.

Files:
- `internal/lsp/server.go`
- `CURRENT_SEMANTICS.md`
- `docs/*`

## Track D: Validation

- [*] Run backend matrix (`llvm`, `qbe`) on smoke files after Tracks A/B changes.
- [*] Run focused semantic tests: raw coercion, cast rules, mut params, ownershipv2 regressions.
- [ ] Update `CTFE_TODO.md` only if these semantics changes touch those roadmaps.

Commands:
- `go test ./internal/analysis/semantics/typechecker`
- `./build.sh`
- `ferret -backend llvm -k -o app <file>.ferr`
- `ferret -backend qbe -k -o app <file>.ferr`

## Track E: Slice + Variadic + String Surface

- [*] Define proper slice type semantics in parser/AST/typeinfo/typechecker, including mutable-slice identity and one-way mut-to-readonly coercion.
- [ ] Fix usage/mutability analysis so slice element mutation counts as using the slice parameter/binding and mutable-slice calls count as mutation on the source binding.
- [*] Add array/slice bounds safety: compile-time diagnostics for provable constant out-of-range indexing, plus runtime panic checks in backend lowering for all remaining array/slice reads and writes.
- [ ] Finish slice value semantics end-to-end: typed literals, indexing, assignment/call compatibility, lowering, and focused runtime tests.
- [ ] Add variadic parameters for both readonly and mutable slice forms on top of the finalized slice model.
- [ ] Add array/slice expansion with `arr...` call syntax after variadic lowering is stable.
- [ ] Finalize `str` as an immutable UTF-8 view only after slice semantics are complete.

Files:
- `internal/frontend/ast/*`
- `internal/frontend/parser/*`
- `internal/analysis/semantics/typeinfo/*`
- `internal/analysis/semantics/typechecker/*`
- `internal/ir/hir/*`
- `internal/ir/mir/*`
- `internal/backend/*`
- `CURRENT_SEMANTICS.md`
