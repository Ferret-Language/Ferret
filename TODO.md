# Compiler TODO

## AST Generation

Current focus: keep MIR stable enough for unwind-aware CFG, usage warnings, and later physical layout without another representation rewrite.

### Frontend foundation

- [x] token model exists
- [x] regex-based lexer exists
- [x] numeric literal tokenization covers more than decimal integers
- [x] parser package exists
- [x] AST node package exists
- [x] syntax diagnostics exist
- [x] multi-file parse pipeline exists
- [x] workspace/module discovery exists
- [x] import graph cycle detection exists
- [x] root-relative import model is enforced
- [x] manifest-based dependency alias loading exists
- [x] stdlib root discovery exists
- [x] stdlib root discovery works for manifest and no-manifest workspaces
- [x] lockfile-aware remote dependency resolution exists
- [x] manifest validation is stricter than placeholder parsing

### Implemented syntax

- [x] imports
- [x] named type declarations
- [x] top-level `const` declarations
- [x] top-level functions
- [x] external methods with receivers
- [x] `let`
- [x] `let mut`
- [x] local `const`
- [x] `return`
- [x] `if`
- [x] `if else`
- [x] `switch`
- [x] blocks
- [x] named and anonymous `struct`
- [x] named and anonymous `interface`
- [x] named and anonymous `enum`
- [x] named and anonymous `union`
- [x] named and anonymous `error`
- [x] tuples
- [x] arrays
- [x] pointer types
- [x] optional types
- [x] error union types
- [x] identifiers and `::` paths
- [x] number literals
- [x] string literals
- [x] `none`
- [x] prefix expressions
- [x] `copy` expressions
- [x] binary expressions
- [x] calls
- [x] calls with type arguments
- [x] selectors
- [x] Zig-style composite literals
- [x] `comptime` parameters
- [x] `comptime` prefix expressions

### Still missing in parsing / AST generation

- [x] module-level `let` declarations
- [x] assignment statements
- [x] compound assignment
- [x] increment / decrement
- [x] Zig-style `for value |v| { }`
- [x] Zig-style `for value |i, v| { }`
- [x] `while`
- [x] `break`
- [x] `continue`
- [x] labels
- [x] labeled `break`
- [x] labeled `continue`
- [x] scope-bound `defer`
- [x] `panic`
- [x] `catch` fallback expression
- [x] `catch` handler block with mandatory early exit
- [x] `lock`
- [x] `unsafe` blocks
- [x] `unsafe` expressions
- [x] `#[builtin]` function declarations without body
- [x] `#[extern("...")]` function declarations without body
- [ ] function literals
- [ ] IIFE syntax
- [ ] closures
- [ ] capture blocks
- [ ] anonymous functions with explicit capture semantics
- [x] import aliasing
- [x] static fields as dedicated AST forms
- [x] constructor syntax
- [x] destructor syntax
- [ ] raw union specific syntax if different from safe union syntax
- [ ] parser recovery improvements
- [x] AST validation pass

### Needs syntax to be frozen before implementation

- [x] exact loop syntax
- [ ] exact label syntax
- [ ] exact closure syntax
- [ ] exact capture block syntax
- [ ] exact IIFE syntax
- [x] exact constructor syntax
- [x] exact destructor syntax
- [x] final rule for module-level `let`
- [x] final static field syntax

## Analyzer Phase

### Collector

- [x] create analyzer package structure
- [x] create module scopes
- [x] create predeclared universe scope
- [x] pre-register `true`, `false`, `none`, and `undefined`
- [x] load builtin function declarations from `ferret_libs_dev/global.ferr`
- [x] load stdlib declarations from `ferret_libs_dev/std/*.ferr`
- [x] basic `std/os` declarations and runtime bindings exist
- [x] `std/os` is runtime-backed, not compiler-constant-backed
- [x] collect top-level symbols
- [x] collect named types
- [x] collect functions and methods
- [x] collect constants

### Resolver

- [x] resolve names across scopes
- [x] resolve imports
- [x] resolve `::` access
- [x] resolve imported visibility / exported-name checks
- [x] resolve enum variants, error members, and static fields through named types
- [x] resolve declaration-side method sets by receiver type
- [x] unresolved symbol diagnostics
- [x] duplicate symbol diagnostics
- [x] cycle-aware module resolution behavior

### Typechecker

- [x] expression type checking
- [x] statement type checking
- [x] `?T` rules
- [x] `E!T` rules
- [x] `!!` rules
- [x] contextual typing for `.{ ... }`
- [x] pointer rules
- [x] method call lookup on typed receivers
- [x] basic receiver compatibility for value, `*T`, and `*mut T` calls
- [x] return statement value checking
- [x] basic `const` / `comptime` evaluation constraints
- [x] visibility rules from naming convention

### Usage Analysis

- [x] define usage-analysis phase boundary
- [x] warn about unused imports
- [x] warn about unused private functions
- [x] warn about unused private types
- [x] warn about unused private module bindings
- [x] warn about unused locals / parameters
- [ ] decide public-API suppression rules for unused symbol warnings

### Ownership Analysis

- [x] define ownership-analysis phase boundary
- [x] move temporary move/borrow checks out of the typechecker
- [x] basic move / copy checking for locals and parameters exists
- [x] use-after-move diagnostics for tracked local bindings exist
- [x] owning receiver consumption rules for tracked local bindings exist
- [x] basic borrow freezing for tracked local bindings exists
- [x] return and module-binding borrow escape diagnostics exist
- [x] ownership now runs after CFG and consumes CFG liveness
- [ ] full move / copy checking across all binding kinds
- [ ] full borrow freezing / escape restrictions
- [x] first flow-sensitive ownership state across branches and loops
- [x] reinitialization and partial-move rules
- [ ] defer/call/aggregate escape tracking

## HIR

- [x] define HIR data model
- [x] lower AST to HIR
- [x] carry builtin / extern linkage metadata into HIR
- [ ] preserve source mapping
- [ ] strip purely syntactic noise

## HIR Lowering

- [x] define lowering rules
- [x] make control flow more explicit
- [ ] desugar frontend constructs

## CFG Analysis

- [x] build CFG from lowered HIR
- [x] reachability analysis
- [x] return-path analysis for non-void functions
- [x] first backward data-flow slice for local-name liveness
- [x] per-block use/def/live-in/live-out sets
- [x] loop-heavy liveness regression tests
- [ ] ownership / liveness support beyond local-name CFG data-flow
- [x] unwind-aware CFG edges for `panic` / `defer`
- [x] cleanup-region modeling for deferred execution during panic unwind
- [ ] final recover/unwind surface design

## MIR

- [x] define MIR data model
- [x] lower globals and CFG functions into MIR
- [x] preserve explicit blocks and control-flow terminators in MIR
- [x] normalize complex values into explicit temporaries
- [x] define MIR validation rules
- [x] local table with stable local IDs
- [x] semantic field-index based projection ops
- [x] carry builtin / extern linkage metadata into MIR
- [x] cleanup/unwind terminators or cleanup-edge modeling
- [ ] separate semantic constants from source literals in MIR globals
- [ ] canonicalize assign/store RHS fully to simple values if strict MIR normalization is desired

## Layout

- [x] define layout phase boundary
- [x] compute size/alignment for builtins
- [x] compute struct layout
- [x] compute semantic-field-index -> physical-offset mapping
- [ ] decide whether field reordering is allowed
- [ ] if reordering is allowed, keep semantic order stable and emit a separate physical layout map
- [ ] layout-aware warnings for padding inefficiency
- [ ] ABI-facing layout export for backend lowering

## Constant Evaluation And Elimination

- [x] constant folding
- [ ] constant propagation
- [x] dead branch elimination
- [ ] compile-time evaluation rules
- [x] decide exact ordering relative to CFG work

## LLVM IR

- [ ] map MIR to LLVM IR
- [ ] preserve source/debug locations where practical
- [ ] define ABI/runtime boundary

## Codegen And Later Work

- [ ] object generation
- [ ] linking flow
- [ ] runtime support
- [ ] FFI support
- [ ] embeddability hooks
- [ ] generated-code testing strategy
- [ ] optimization pipeline decisions
- [ ] packaging and tooling

## Package And Tooling Infrastructure

- [ ] manifest diagnostics with source spans
- [ ] remote fetch/install workflow
- [ ] checksum validation for cached remote modules
- [ ] lockfile writing and update flow
- [ ] dependency version conflict handling
- [ ] stdlib/toolchain root policy finalization
- [ ] freeze source syntax for target-conditional attributes such as `#[if-arch(...)]`
- [x] named types may be explicitly marked `move`

## Intended Order

- [x] AST generation started first
- [x] analyzer phase
- [x] HIR
- [x] HIR lowering
- [x] CFG analysis
- [ ] constant evaluation and elimination
- [x] MIR
- [x] usage analysis
- [x] layout
- [ ] LLVM IR
- [ ] codegen and later work
