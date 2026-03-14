# Semantics Refactor Report (Legacy vs Current)

This repo contains:

- The current compiler under `compiler/` (Go module `compiler`).
- Reference/legacy snapshots at the repo root: `semantics_old_compiler/`, `pipeline_old_compiler/`, `phase_old_compiler/`.

This report focuses on why the current semantic analysis feels hard to follow, how the legacy approach kept flow “single source”, and a refactor plan that keeps the current architectural rules (especially “no semantic state in AST”) while reducing duplicated state and improving traceability.

## 1. What The Current Compiler Does Today

### 1.1 Phase Order And Data Flow

The v2 pipeline parses modules in parallel, then runs semantic passes sequentially in topological order. The core per-module semantic flow is:

```
AST
  -> collector: ModuleScope + MethodSets + TypeMembers
  -> resolver:  Bindings (node -> symbol/module resolution, locals list, labels)
  -> typechecker: Types (node -> type, symbol -> type)
  -> HIR/MIR/ownership/... (later stages)
```

Relevant code:

- `compiler/internal/pipeline/pipeline.go` (`runSemanticPasses`).
- `compiler/internal/semantics/collector/collector.go` (module-level symbol collection).
- `compiler/internal/semantics/resolver/resolver.go` (name binding + local symbol creation).
- `compiler/internal/semantics/typechecker/typechecker.go` (type checking + type synthesis + narrowing).

### 1.2 “Storage Everywhere”: What’s Actually Stored

The `context.Module` object is the “phase handoff surface” and currently contains:

- Syntax: `Tokens`, `AST`
- Semantic indices: `ModuleScope` (`*table.Scope`), `MethodSets`, `TypeMembers`
- Semantic results: `Bindings` (`*binding.ModuleInfo`), `Types` (`*typeinfo.ModuleInfo`)
- IRs: `HIR`, `LoweredHIR`, `CFG`, `MIR`, `Layout`

See: `compiler/internal/context/context.go` (`type Module struct { ... }`).

This is a reasonable pattern for a compiler, but maintainability depends on avoiding duplicated representations of the same concept.

### 1.3 The Specific Pain Point: Local Value Environments

In the current compiler, locals are represented in multiple, not-quite-aligned ways:

- Resolver builds a lexical `*table.Scope` as it walks statements and declares locals (it is not persisted).
  - It also records a flat list of local symbols per function: `Bindings.FunctionLocals`.
  - It binds *identifier uses* (and type paths) to `*symbols.Symbol` via `Bindings.Nodes[node]`.
  - See: `compiler/internal/semantics/resolver/resolver.go`.

- Typechecker does not re-use resolver’s local scopes. Instead it re-creates a separate local environment:
  - `valueScope` keyed by string name with `valueInfo{typ, mutable, constant}`.
  - It is used for:
    - sequential local type inference (`let x = ...` then use `x`)
    - mutability/const checks in assignment and addressability
    - narrowing overlays for `is` and match arms
  - See: `compiler/internal/semantics/typechecker/typechecker.go` (near the top, plus `narrowedScopeForCondition`).

This duplication is the main reason the “code flow” feels hard:

- The resolver and typechecker must stay in lockstep about *where* bindings are introduced (block scopes, match arms, `for` iterator names, `lock` binder, catch payloads, etc).
- When they drift, you get hard-to-debug “resolver says it binds, typechecker says it’s missing” or the reverse.
- Locals are keyed by `string` in the typechecker, but by `*symbols.Symbol` in bindings/types maps, which makes cross-phase reasoning harder than it needs to be.

## 2. How The Legacy Compiler Solved The Same Problem

The legacy snapshot takes a more “single-source, threaded state” approach:

- A reusable symbol table (`table.SymbolTable`) is threaded through collector/resolver/typechecker.
  - See: `semantics_old_compiler/table/symbolTable.go`.

- `symbols.Symbol` carries semantic state directly (including `Type`, `OriginalType`, readonly/heap flags, const value).
  - See: `semantics_old_compiler/symbols/sym.go`.

- Narrowing works by mutating symbol type state (and remembering original types), rather than maintaining a separate map.

This makes the flow easier to follow because there is one authoritative local environment:

```
Scope chain (symbols) is the value environment,
and symbols themselves carry types/mutability.
```

Tradeoff: it tightly couples phases and makes it easy to accidentally leak “semantic decisions” into earlier phases (or create hidden cross-phase dependencies).

## 3. Refactor Goals (For The Current Compiler)

The intent should be:

- Keep the current “separate semantic info” design (per `compiler/COMPILER_GUIDELINES.md`).
- Remove duplicated representations of locals across phases.
- Make every value’s identity stable across passes (avoid name-based local typing decisions).
- Make narrowing and mutability checks explicit, local, and easy to test.

Non-goals:

- Reverting to “types live on symbols” or “scopes live on AST nodes”.
- A full pipeline redesign (HIR/MIR/etc are out of scope for this report).

## 4. Proposed Target Architecture (Semantics Only)

### 4.1 Single Source Of Truth For Name Binding

Make `resolver` the sole authority on “what identifier refers to what symbol/module”.

Concrete rule:

- Every `*ast.Ident` that appears in syntax should have a `Bindings.Nodes[...]` entry when resolvable, including:
  - identifier *uses* (already true)
  - identifier *declarations* (currently not consistently bound)

Why this matters:

- Typechecking can then be “symbol-first” instead of “string-name-first”.
- Shadowing becomes robust because symbol identity differs even when names are equal.

### 4.2 Single Source Of Truth For Types

Make `typeinfo.ModuleInfo.Symbols[*symbols.Symbol] = typeinfo.Type` the single source of truth for value types (including locals).

Concrete rule:

- When typechecking a local binder statement (`let`, `const`, loop iter vars, catch payload), the typechecker must:
  - compute the binder’s final type in program order
  - bind that type to the binder’s `*symbols.Symbol` in `Types.Symbols`

Then `typeOfIdent` can always do:

```
resolution := Bindings.Nodes[ident]
symbolType := Types.Symbols[resolution.Symbol]
```

and never need a parallel environment of name -> type.

### 4.3 Narrowing As A Refinement Overlay (Not A Parallel Environment)

Narrowing is the one semantic feature that genuinely benefits from an overlay structure.

Instead of `valueScope{name -> valueInfo{typ,...}}`, use a dedicated refinement layer:

- `refinementScope`:
  - parent pointer (lexical nesting)
  - `types map[*symbols.Symbol]typeinfo.Type` for “narrowed override types”

Lookup rule:

- If a symbol has a refined type in the overlay chain, use it; otherwise use the base type from `Types.Symbols`.

This keeps narrowing local to condition branches without duplicating the base environment.

### 4.4 Mutability / Constness As First-Class Binder Metadata

The typechecker currently needs “is mutable?” in order to decide addressability and assignment legality.

Avoid re-encoding this per-phase. Pick one stable representation:

Option A (recommended, minimal moving parts):

- Extend `symbols.Symbol` to include:
  - `Mutable bool` (for `let mut`, lock binders, receiver if applicable)
  - `Constant bool` can remain derived from `Kind == SymbolConst`

Option B (more purist separation):

- Extend `binding.ModuleInfo` with:
  - `LocalAttrs map[*symbols.Symbol]LocalAttr{Mutable bool, ...}`

Option A reduces cross-struct lookups and keeps “binder facts” on the symbol identity that already flows through the compiler.

## 5. What This Refactor Removes / Simplifies

With the target architecture:

- `typechecker.valueScope` and `typechecker.valueInfo` can be removed or reduced to a tiny refinement-only overlay.
- `typechecker.typeOfLocalIdent` becomes unnecessary; locals are typed via symbol identity and `Types.Symbols`.
- Shadowing and “same-name different binding” bugs become much harder to introduce.
- Resolver/typechecker drift risk drops: the typechecker no longer re-derives “what is in scope”; it relies on resolver bindings.

## 6. Concrete Migration Plan (Low Risk First)

### Step 1: Bind Declaration Idents In The Resolver

In `compiler/internal/semantics/resolver/resolver.go`, whenever a local binder symbol is created, also bind the declaration `*ast.Ident` node to that symbol:

- `let` / `const` statements: bind `s.Name` to the created symbol
- receiver: bind `d.Receiver.Name`
- params: bind `param.Name`
- `for` binders (`Index`, `Value`): bind those idents
- `lock` binder: bind `s.Name`
- catch payload binder (if it exists in syntax): bind it

This does not change language behavior; it only makes symbol identity accessible to later passes.

### Step 2: Bind Types For Local Symbols During Typechecking

In `compiler/internal/semantics/typechecker/typechecker.go`:

- When a binder’s type is computed (e.g. `LetStmt` final type, `ConstStmt` final type, params/receiver types), do:
  - `c.info.BindSymbol(localSym, finalType)`

This makes local symbol types discoverable via `typeOfSymbol` and also enables later passes to query types without re-walking statements.

### Step 3: Replace Name-Keyed Local Lookups With Symbol-Keyed Lookups

Update `typeOfIdent`:

- Remove the “if len(path)==1 consult valueScope” branch.
- Always consult `Bindings.Nodes[ident]` to get the resolved `*symbols.Symbol`.
- Retrieve type from `Types.Symbols` (with refinement overlay applied).

At this point, the typechecker no longer needs a base “locals map” at all.

### Step 4: Introduce A Small Refinement Overlay For Narrowing

Replace `narrowedScopeForCondition` and match-arm narrowing logic to produce `refinementScope` overlays keyed by `*symbols.Symbol`.

This overlay should only store type overrides (no mutability/const/base type).

### Step 5: Centralize Mutability Facts

Pick Option A or B from section 4.4 and remove `valueInfo.mutable` / `valueInfo.constant` from typechecker state.

### Step 6: Update Ownership And Usage (As Needed)

`usage` already walks `Bindings.FunctionLocals` and symbol names, so it likely keeps working.

`ownership` is MIR/CFG-based and currently keying by local name; this is a separate concern. Don’t try to merge it into the typechecker refactor. If MIR locals can shadow, consider switching ownership’s map key from name to a stable local ID (MIR already appears to have `LocalID`s).

## 7. Risks And How To Control Them

Main risk: incomplete binding coverage.

- If the resolver does not bind declaration idents, typechecker cannot reliably map binder nodes to the correct symbol.
- Mitigation: add resolver tests that assert `Bindings.Nodes` entries exist for binder idents.

Main risk: “typeOfSymbol synthesizes types by re-typing initializer with nil scope”.

- Mitigation: after Step 2, locals should always have an explicit `Types.Symbols[sym]` entry, so `typeOfSymbol` won’t fall back to synthesis for locals.

Narrowing correctness risk:

- Overlay should affect only the branch it is created for.
- Mitigation: add tests for `if x is T { ... } else { ... }` where `x` has different inferred types inside branches.

## 8. Why `valueScope` Is Not “Wrong”, Just A Symptom

A typechecker must carry some notion of “what do names mean” and “what types do locals have so far”.

`valueScope` exists because:

- `typeOfSymbol` cannot infer local types correctly without a local environment (it calls `typeOfExpr(nil, ...)`).
- The typechecker needs program-order local type inference and per-branch narrowing.

The refactor above keeps those needs, but moves them onto:

- resolver bindings (symbol identity)
- `Types.Symbols` (authoritative types)
- a tiny refinement overlay (narrowing only)

This cuts the duplicated “locals environment” down to the one part that actually benefits from an overlay: narrowing.

