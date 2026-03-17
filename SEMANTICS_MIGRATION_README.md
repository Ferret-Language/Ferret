# Ferret Semantics Migration Plan

This plan targets `Ferret-compiler-v2/compiler` only.

## Status

[*] Analyzed the current compiler entry path from `cmd/ferret/main.go` into the real semantic pipeline.
[*] Confirmed semantic passes run in this order: resolver -> typechecker -> HIR -> CFG/MIR -> ownership.
[*] Mapped the current model: `*T`, `*mut T`, `*own T`, `*raw T`, explicit `copy`/`take`, move-marked named types, pointer-based borrows, receiver auto-borrow.
[*] Compared the current model against `task.md` and identified the main incompatibilities.
[*] Identified the primary migration hotspots: parser/AST, typeinfo, method lookup, interface matching, ownership, HIR/MIR, tests, docs.

## Target Model Summary

[*] `T` = stack value, copyable, passed by value.
[*] `*T` = owning heap pointer, non-null, move-only.
[*] `&T` = immutable borrowed reference, stack-only, non-owning.
[*] `&mut T` = mutable borrowed reference, stack-only, non-owning.
[*] `^T` = raw pointer, unsafe only.
[*] Parameter passing defines semantics for values, parameters, and receivers.
[*] Interfaces must include receiver modifier in the required method signature.
[*] Heap values must not contain references.

## Incremental Migration Phases

### Phase 1: Introduce the new surface syntax and type model

[*] Replace the old pointer AST/type model with explicit categories for owning pointer, reference, mutable reference, and raw pointer.
[*] Update the lexer/parser so the accepted type forms become `T`, `*T`, `&T`, `&mut T`, and `^T`.
[*] Remove parser support for old safe pointer spellings `*mut T`, `*own T`, and `*raw T`.
[*] Remove parser support for `type Name move ...`.
[*] Update AST debug/dump output to reflect the new type forms.
[*] Update type lowering in the typechecker so syntax maps to the new semantic types.
[*] Update type formatting and equality logic to reflect the new type identities.
[*] Add parser and typeinfo tests for all new type forms.

### Phase 2: Make copy/move classification match the new rules

[*] Redefine move semantics so only `*T` is move-only by default.
[*] Redefine copy semantics so plain `T` values are copyable by default.
[*] Remove structural move-by-default behavior for structs, tuples, arrays, unions, interfaces, optionals, and error unions.
[*] Remove dependence on move-marked named types from semantic analysis.
[*] Decide whether `copy` remains as optional explicit syntax or is removed entirely.
[*] Remove or redefine `take` so it no longer carries the old move-model assumptions.
[*] Update ownership tests to match the new move/copy rules.

### Phase 3: Separate references from pointers

[*] Introduce distinct semantic types for `&T` and `&mut T`; they must not be represented as ordinary pointers.
[*] Update prefix expression typing so `&` and `&mut` produce reference types, not pointer types.
[*] Update dereference and field/index access rules so they operate correctly on owning pointers, references, and raw pointers.
[*] Enforce that `^T` operations requiring safety are allowed only in `unsafe` contexts.
[*] Add diagnostics for illegal reference usage where the current compiler still assumes pointer behavior.

### Phase 4: Rework parameter and receiver semantics around one rule

[*] Update function parameter semantics so `T`, `*T`, `&T`, and `&mut T` fully define copy/move/borrow behavior.
[*] Replace old receiver matching keys (`*T`, `*mut T`, `*own T`) with the new receiver forms (`T`, `*T`, `&T`, `&mut T`).
[*] Remove old method-call auto-borrow behavior that was designed for pointer receivers.
[*] Revisit call-site lowering so receiver passing is just ordinary first-parameter lowering under the new rules.
[*] Update constructor/destructor special cases if they still exist after the receiver model change.
[*] Add focused tests for each receiver form and each allowed/disallowed call pattern.

### Phase 5: Redesign interface method signatures

[*] Extend interface AST nodes to store receiver modifier explicitly.
[*] Update interface parsing so declarations like `&mut read(buf []u8) i32` are accepted and preserved.
[*] Extend semantic interface method types so receiver modifier participates in identity.
[*] Update interface satisfaction checks to require name, receiver modifier, parameters, and result to all match.
[*] Update collector/method-set indexing so interface matching can query receiver-form-specific methods precisely.
[*] Update HIR/MIR interface type declarations to keep receiver modifier information.
[*] Add tests for interface satisfaction success/failure based on receiver modifier.

### Phase 6: Rewrite ownership analysis around owners and borrows

[*] Rebuild ownership classification so only owning heap pointers (`*T`) are consumed by moves.
[*] Keep use-after-move diagnostics for owners, but stop treating ordinary value types as move-only by default.
[*] Rebuild borrow tracking around reference types instead of pointer-shaped borrows.
[*] Enforce single active `&mut` borrow and no overlapping mutable/immutable borrows.
[*] Enforce that references cannot escape scope by return, module binding, heap storage, or deferred capture.
[*] Enforce that heap values cannot contain `&T` or `&mut T`.
[*] Revisit partial-move logic so it only applies where the new model actually needs it.
[*] Expand ownership tests to cover local scopes, branches, loops, returns, globals, and aggregate storage.

### Phase 7: Update assignment, mutation, and access rules

[*] Make binding mutability (`let mut`) the gate for reassignment and mutation.
[*] Ensure mutation through `&mut T` and owning pointers is allowed only with mutable access.
[*] Ensure immutable references and immutable bindings reject mutation consistently.
[*] Review selector, dereference, and assignment-target checks so they respect the new access model.
[*] Add tests for field mutation, pointer mutation, reassignment, and immutable access failures.

### Phase 8: Propagate the new semantics through HIR and MIR

[*] Update HIR generation so it no longer assumes old pointer kinds or old implicit receiver borrowing.
[*] Update MIR lowering and normalization to represent new owner/reference/raw operations cleanly.
[*] Remove IR-level assumptions tied to `*own`, `*mut`, and `*raw`.
[*] Review deferred destructor generation and any ownership-triggered synthetic IR for compatibility with the new model.
[*] Keep MIR validation passing after each semantic slice lands.

### Phase 9: Adapt backends and runtime contracts

[*] Audit LLVM and QBE lowering for assumptions about old pointer kinds, interface receiver layout, and auto-borrowed calls.
[*] Update backend type lowering for owning pointers, references, and raw pointers as separate semantic categories.
[*] Revisit interface lowering if receiver modifiers affect wrapper generation or dispatch layout.
[ ] Revisit runtime headers/comments so they describe the new semantics accurately.
[ ] Keep backend changes minimal until HIR/MIR semantics are stable.

### Phase 10: Cleanup, compatibility removal, and stabilization

[ ] Remove old syntax, old tests, and dead compatibility code once the new model is working end-to-end.
[ ] Update `supported.md`, `LANG_V0_1_CORE.md`, `COMPILER_GUIDELINES.md`, and other docs to match the new semantics.
[ ] Add end-to-end examples demonstrating `*T`, `&T`, `&mut T`, and `^T`.
[ ] Run and fix parser, typechecker, ownership, MIR, and backend test suites.
[ ] Add regression tests for the exact rules from `task.md`.
[ ] Mark this migration complete only after the old pointer/ownership model is fully removed.

## Recommended Execution Order

[*] Start with syntax and semantic type representation first.
[*] Then update copy/move classification before touching deeper ownership behavior.
[*] Then change method/receiver and interface matching together so the callable surface stays coherent.
[*] Then rewrite ownership around the new owner/reference split.
[*] Then propagate the new model through HIR/MIR and only after that adjust backend lowering.
[ ] Execute Phase 1 through Phase 5 before attempting a full end-to-end compile of migrated examples.
[ ] Keep each phase small enough to land with passing targeted tests.

## Concrete First Implementation Slice

[*] Add new type nodes or fields for owning pointer, immutable reference, mutable reference, and raw pointer.
[*] Update parser/typechecker/typeinfo to accept and print the new types.
[ ] Keep backends untouched in the first slice except for whatever is needed to compile tests for parsing/type formation.
[*] Add parser tests for `*T`, `&T`, `&mut T`, and `^T`.
[*] Add typechecker tests that prove `&` no longer means ordinary pointer type creation.
