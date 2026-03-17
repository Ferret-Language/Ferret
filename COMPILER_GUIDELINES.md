# Compiler Guidelines

This file exists to keep the compiler implementation coherent over time.

The goal is not to copy Ferret or any other compiler blindly. The goal is to build a compiler that is:

- correct
- easy to extend
- easy to reason about
- organized around clear phase boundaries

## 1. Core Rule

Do not cargo-cult architecture.

When reusing ideas from Ferret or any other compiler:

- copy the idea only if it fits this language
- simplify when the full design is not needed yet
- avoid premature generalization
- keep room for later semantic and codegen phases

If a design is copied, there must be a concrete reason for it.

Do not ship placeholder architecture for core compiler subsystems.

- parsing, manifests, module resolution, dependency loading, semantic analysis, IR, and codegen must be designed for real compiler growth
- avoid “basic for now” implementations that will need to be thrown away once the compiler handles real projects
- when a subsystem is important, implement the correct boundary and data model first, even if some features inside that boundary remain incomplete
- small scope is acceptable; toy structure is not

## Reference Repositories

The old Ferret compiler, grammar, and editor support repos are reference material only.

- use them to understand solved problems
- use them to compare design tradeoffs
- do not mirror their architecture by default
- do not copy syntax decisions that no longer match this language
- do not cargo-cult generated structure, package layout, or editor integration details

For this repo specifically:

- old Ferret is a historical reference
- new Ferret is a separate language revision
- grammar and editor support should be reimplemented for the new syntax, not patched around old assumptions

## Library Source Of Truth

Library source files live in `ferret_libs_dev/`.

- `ferret_libs_dev/` is the editable source tree for builtin Ferret libraries
- the build tooling has a single mode: `go run ./tools` (also used by `build.sh` / `build.bat`)
- all generated artifacts are placed under `output/ferret/`
- the compiler binary is produced at `output/ferret/bin/ferret` (or `.exe` on Windows)
- builtin libs and runtime archive are produced at `output/ferret/libs/`
- backend toolchain binaries and shared libs are bundled under `output/ferret/toolchain/`
- root-level `bin/` and `libs/` are legacy and are removed by the unified build flow
- do not edit generated files under `output/ferret/` directly

## 2. Project Priorities

In order:

1. correctness
2. clarity of semantics
3. maintainability
4. diagnostic quality
5. performance

Do not trade away 1-4 for micro-optimizations.

For critical infrastructure, prefer durability over short-term speed of implementation.

- a smaller but structurally correct subsystem is acceptable
- a quick implementation that bakes in the wrong model is not

## 3. Language Context To Preserve

These decisions are already part of the language design and should not drift accidentally:

- `::` is used for imported names, enum variants, and static members
- imports are package-root-relative, never relative
- `std/...` is reserved for the standard library
- dependency imports are resolved by manifest alias on the first path segment
- a non-aliased import binds the last path segment in source, for example `import "util/build"` binds `build`
- Zig-style literals are used: `.{ ... }`
- methods are declared outside types using receivers: `fn (p Point) name(...)`
- there is no implicit `self`
- `?T` is built-in optional
- `E!T` is built-in error union
- `!!` is the error propagation syntax
- pointer ownership is explicit:
  - `*T`
  - `&T`
  - `&mut T`
  - `^T`
- `defer` and `panic` are part of the core control-flow model
- builtin functions are declared in `ferret_libs_dev/global.ferr`
- stdlib source modules are declared in `ferret_libs_dev/std/*.ferr`
- builtin declarations use `#[builtin]` and may omit a body
- external declarations use `#[extern(\"...\")]` and may omit a body
- error unions are explicit value-level control flow and are not exceptions

If implementation changes conflict with this, update the language spec first.

## 4. Architecture Rules

The compiler should be split into clear layers.

- `internal/frontend/lexer`: tokenization only
- `internal/frontend/parser`: syntax only
- `internal/context`: module table, dependency graph, caches, shared compiler state
- `internal/pipeline`: phase orchestration
- `internal/middleend/hir`: all HIR data structures and HIR-local transforms
- `internal/middleend/mir`: MIR and MIR-local transforms
- `internal/semantics`: name resolution, type checking, ownership checks
- future `internal/layout`: physical type layout, alignment, size, and field-slot mapping
- `internal/cfg`: CFG data model only
- `internal/cfganalysis`: CFG construction and CFG-based analyses
- future `internal/codegen`: lowering and backend work

No package should mix all of these concerns.

HIR-specific rule:

- do not split HIR model, HIR generation, and HIR lowering across unrelated top-level packages
- keep HIR-related code under `internal/middleend/hir`
- if HIR grows, prefer subpackages under `internal/middleend/hir/...` over creating parallel top-level `hir*` packages again

## 5. Lexer Rules

The lexer is not a parser.

- tokenize only
- no semantic decisions
- no type inference
- no ownership analysis
- no parser-level hacks

Use regex/token-pattern driven tokenization when it improves clarity.
Do not use regex just because another compiler did. Use it only when the token class is naturally pattern-based, such as numeric literals.

When adding token support:

- keep token names general enough for later phases
- prefer `NUMBER` over fake subcategories unless syntax requires the split
- keep literals normalized only when that is clearly beneficial
- do not silently erase information needed by later phases

## 6. Parser Rules

The parser should build syntax, not interpretation.

- keep it single-responsibility
- keep it readable without helper noise
- avoid wrapper functions whose only job is to rename token checks
- recovery should exist, but not at the cost of unreadable control flow

Parser code should answer:

- what syntactic form is being parsed
- what tokens are consumed
- what AST node is produced

If a function makes that hard to see, rewrite it.

## 7. AST Rules

AST nodes represent source structure, not semantic conclusions.

- avoid embedding semantic state in frontend AST nodes
- keep locations on all user-visible nodes
- choose names that match syntax, not later implementation details

Examples:

- `NumberLit` is better than `IntLit` if the lexer accepts non-integer numerics
- `ImportDecl` should exist if imports are part of module syntax

## 8. Context And Pipeline Rules

Use a central compiler context for shared state.

The context owns:

- modules
- file/import mapping
- dependency graph
- source cache
- diagnostics
- incremental state

Module identity inside the compiler must be stronger than raw source import text.

- use origin-qualified module keys internally
- do not let local, stdlib, and dependency modules collide in one string namespace
- reject relative imports at resolution time

Manifest and package-resolution logic should stay outside `context` and `pipeline`.

- manifest parsing belongs in a dedicated package
- project/workspace loading may feed config into the compiler context
- fetching/downloading is a separate concern from parsing and import-graph construction

The pipeline owns:

- phase order
- loading modules
- traversing imports
- cache reuse decisions
- cycle detection

Do not hide pipeline behavior inside parser or lexer code.

## 8.1 Phase Responsibilities

Phase ownership must stay explicit.

- `collector`
  - create module scopes
  - collect top-level names
  - collect method sets by receiver
  - no name lookup beyond local declaration registration
- `resolver`
  - resolve imports
  - resolve local and nested names
  - resolve `::` paths
  - enforce cross-module visibility for imported names
  - bind named-type members that are syntactically addressable without type inference, such as enum variants and static fields
  - bind labeled control-flow targets
  - no type inference or data-flow reasoning
- `typechecker`
  - type expressions and statements
  - validate assignment compatibility
  - validate return statement value types against function result types
  - perform call-site method lookup once receiver types are known
  - validate pointer, optional, and error-union rules
  - do local, type-directed checks only
  - do not do full path-sensitive return completeness here
- `usage analysis`
  - run as a final whole-compilation pass after ownership on clean modules
  - warn about unused imports
  - warn about unused private module symbols such as private functions, types, and module bindings
  - warn about unused locals and parameters where the language wants those diagnostics
  - treat public API surface differently from private/internal declarations
  - do not own type layout or backend-specific liveness decisions
- `ownership analysis`
  - run after MIR generation on typed, normalized IR
  - validate moves, borrow freezing, and borrow escape rules
  - own use-after-move diagnostics and ownership-state transitions
  - consume CFG liveness once CFG exists instead of recursing purely over syntax
  - use MIR temp/place provenance instead of re-reading frontend syntax
  - handle path-sensitive and flow-sensitive reasoning
  - stay separate from the basic typechecker
- `HIR`
  - preserve typed semantics while removing parser-only surface noise
  - represent control constructs in a form suitable for CFG construction
- `HIR lowering`
  - desugar frontend sugar
  - normalize constructs before CFG
- `CFG analysis`
  - build per-function control-flow graphs
  - do reachability and return-path analysis
  - decide whether non-void functions return on all paths
  - handle nested conditionals and loops through graph analysis, not ad hoc AST recursion
  - own unreachable-code diagnostics once CFG exists
  - be unwind-aware once `panic` / `defer` lowering is implemented
  - distinguish normal edges from panic-unwind cleanup edges
- `const evaluation`
  - fold and propagate only after types are known
  - may inform CFG simplification, but should not replace CFG-based correctness checks
- `layout`
  - compute physical size, alignment, and field offsets
  - may reorder struct fields physically for packing/alignment if the language permits it
  - must not mutate semantic/source field order used by parser, typechecker, HIR, ownership, or diagnostics
  - must produce a stable mapping from semantic field index to physical slot/offset
  - must own ABI-facing layout decisions, not the typechecker or MIR builder

Return-path analysis belongs to CFG analysis, not the basic typechecker.

Reason:

- it is fundamentally a control-flow problem
- nested loops and branching make AST-local reasoning brittle
- CFG gives one place to handle `if`, `switch`, loops, `break`, `continue`, and labels coherently

Ownership and borrow checking should not be treated as part of the basic typechecker contract.

Reason:

- it needs flow-sensitive state transitions
- it interacts with control flow, reinitialization, and escapes
- keeping it separate makes the typechecker simpler and keeps ownership logic aligned with later CFG/data-flow work

## 8.2 Unwind And Error Model

Do not conflate `panic` with `E!T`.

- `E!T` is explicit value-level control flow
- `!!` lowers to ordinary control flow
- `panic` is non-local control flow
- `defer` runs on both normal exit and panic unwind
- `recover` is not current surface syntax; if reintroduced later it must fit the unwind/cleanup model cleanly

This implies:

- CFG and MIR must eventually support cleanup/unwind edges
- MIR terminators must remain extensible; do not hard-code the assumption that only normal branch/jump/return exist forever
- cleanup execution order must be explicit in IR, not reconstructed from source text late in codegen

Do not fake panic semantics by lowering it to an ordinary call and hoping codegen reconstructs unwind behavior later.

## 8.3 Semantic Order vs Physical Layout

Semantic field order and physical field layout are different concepts.

- semantic order is the declared/source order
- semantic field indices in HIR/MIR should remain stable for diagnostics, ownership, and language semantics
- physical layout is computed later by a dedicated layout phase

If field reordering is allowed for packing/alignment:

- keep semantic field index stable
- compute a separate semantic-index -> physical-slot/offset mapping
- do not rewrite HIR/MIR/ownership to use physical order directly

This avoids breaking:

- dumps
- diagnostics
- source mapping
- ownership/partial-move reasoning on fields
- cross-phase reproducibility

## 8.4 Warning Ownership

Warnings must have a clear owner phase.

- unused imports: resolver or usage analysis
- unused private top-level symbols: usage analysis
- unused locals/params: CFG/usage analysis, not parser
- layout inefficiency warnings: layout phase

Do not scatter warning heuristics across unrelated passes.

Temporary checks in the typechecker are acceptable only as a bridge while the dedicated phase is being built. They should not define the long-term architecture.

## 9. Incremental And Cache Rules

Incremental behavior must be explicit and testable.

- cache keys must be obvious
- module invalidation rules must be local and understandable
- unchanged modules should be reusable
- dependency changes must invalidate correctly

Do not build a “smart cache” that nobody can reason about.

## 10. Diagnostics Rules

Diagnostics are a product feature, not a side effect.

- every syntax error should point to a concrete span
- messages should say what is wrong and what was expected
- error codes should remain stable once exposed
- avoid generic “unexpected token” if a better message is easy to give

Prefer a smaller number of accurate diagnostics over a flood of cascading noise.

## 11. Testing Rules

Every non-trivial frontend change should come with tests.

Minimum expectations:

- lexer tests for new token forms
- parser tests for new syntax forms
- pipeline tests for multi-file behavior
- regression tests for previously broken cases

Do not rely on one example file as proof that the compiler works.

## 12. Change Discipline

Before changing code, ask:

1. Is this syntax, parsing, semantics, or orchestration?
2. Which package should own it?
3. Will this decision still make sense after type checking exists?
4. Am I encoding a shortcut that will become a bug later?

If the answer to 4 is yes, do not do it.

## 13. Anti-Patterns

Avoid these:

- helper layers that only rename obvious operations
- parser code that depends on semantic facts
- token kinds that encode typechecker assumptions
- architecture copied from another compiler without current need
- one-file “temporary” logic that becomes permanent
- hidden ownership rules
- silent implicit behavior that the language spec does not justify

## 14. Preferred Style

Prefer:

- small focused packages
- direct names
- explicit data flow
- phase-local responsibilities
- tests next to the behavior they protect

If a simpler design solves the problem cleanly and preserves the right long-term structure, prefer it over ornamental complexity.
Do not confuse “simple” with “throwaway” or “underdesigned”.

## 15. Workflow Rule

When implementing new compiler work:

1. check this file
2. check `LANG_V0_1_CORE.md`
3. decide which layer owns the change
4. choose a design that will survive analyzer/codegen stages
5. implement the smallest structurally correct version
6. add tests
7. only then generalize if there is real pressure
