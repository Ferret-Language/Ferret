# Ferret Compiler Development Guide

## Project Overview

Ferret is a Go-based compiler for a modern systems programming language with:
- Go-style AST architecture
- Rust-style error diagnostics with rich source context
- Module system with import path resolution (local, builtin, remote)
- Parallel compilation pipeline with unbounded goroutines (one per module)

**Module:** `compiler` (not a typical `github.com/...` path)

## Architecture

### Compilation Pipeline (internal/pipeline/)

The compiler uses a **multi-phase pipeline** where each module progresses through phases independently:

```go
// Module phase progression
PhaseNotStarted → PhaseLexed → PhaseParsed → PhaseCollected → PhaseResolved → PhaseTypeChecked → PhaseCodeGen (future)
```

**Pipeline Phases:**
1. **Lex + Parse** - Parallel parsing with deduplication by import path
2. **Symbol Collection** (`internal/semantics/collector/`) - Build symbol tables, declare names
3. **Resolution** (`internal/semantics/resolver/`) - Bind identifiers to declarations
4. **Type Checking** (`internal/semantics/typechecker/`) - Validate types and control flow
5. **Code Generation** (future)

**Critical invariants** (enforced by `pipeline_test.go`):
- Each module is parsed exactly once (deduplicated by import path)
- Diagnostic collection is thread-safe (`sync.Mutex` in `DiagnosticBag`)
- Phase advancement requires prerequisites (checked via `phasePrerequisites` map)
- Phases run in topological order after parallel parsing completes

Example: A module at `PhaseResolved` requires all imports at least `PhaseParsed`.

### Module System (internal/context_v2/)

**Import path semantics** (not filesystem paths):
- `"test_project/utils"` → `{ProjectRoot}/test_project/utils.fer`
- `"std/math"` → `{BuiltinModulesPath}/std/math.fer`
- Future: `"github.com/user/pkg"` → Remote dependency

**Key types:**
- `CompilerContext`: Central state manager with `Modules` map (import path → Module)
- `Module`: Holds `ImportPath`, `FilePath`, `Type`, `Phase`, `AST`, `Symbols`, and `Mu` (mutex for field updates)
- `SymbolTable`: Lexical scope chain for symbol resolution

Import cycle detection uses `DepGraph` (map of import path → dependencies).
Dependencies are automatically deduplicated to avoid redundant edges.

### Error Diagnostics (internal/diagnostics/)

Rust-style error output with:
- Primary/secondary labels with `^ or ~~~` and `---` underlines
- Color-coded severity (Error, Warning, Info, Hint)
- Source context with line numbers
- Notes and help suggestions

```go
diagnostics.NewError("message").
    WithPrimaryLabel(filepath, location, "explanation").
    WithSecondaryLabel(otherFile, otherLoc, "context").
    WithHelp("try doing X instead").
    WithNote("additional info")
```

Thread-safe collection via `DiagnosticBag.Add()` with internal mutex.
**SourceCache** (for error reporting) also uses `RWMutex` for concurrent access.

### Lexer (internal/frontend/lexer/)

Regex-based tokenizer (`tokenizer.go`) that:
- Continues on errors instead of panicking (adds to `DiagnosticBag`)
- Reports unrecognized characters as diagnostics
- Skips invalid characters and continues tokenizing
- Allows parser to find additional errors downstream

### AST Structure (internal/frontend/ast/)

**Interface hierarchy:**
- `Node` (base) → `Expression`, `Statement`, `Decl`, `TypeNode`, `BlockConstruct`
- Locations stored as `source.Location` with `Start` and `End` positions

**Parser state tracking:**
- `seenNonImport` flag enforces imports-first ordering
- Error recovery: reports error and continues parsing (minimal recovery)
- Special handling for missing semicolons (points to insertion location)

## Development Workflows

### Build & Run
scripts folder contains build scripts:
```bash
# Build native
./build.sh|bat
# Build all (native + WASM)
./build_all.sh|bat
```

# Run directly (development)
go run main.go test_project/main.fer

# With debug output
go run main.go -d test_project/main.fer
```

### Testing

Standard Go tests with table-driven patterns:

```bash
go test ./...                          # All tests
go test -v ./internal/pipeline         # Verbose output
go test -run TestPipelineInvariants    # Specific test
```

**Test coverage:**
- `pipeline_test.go`: Phase invariants, cycle detection, parallel safety
- `*_test.go`: Unit tests follow Go conventions (no external frameworks)

### Module Resolution

Entry point resolution order:
1. Check `Content` field (in-memory/WASM mode)
2. Resolve import path to file path via `ImportPathToFilePath()`
3. Read file, tokenize, parse
4. Extract imports from AST, recursively process

**Builtin modules:** Located in `ferret_libs/` (e.g., `ferret_libs/std/math.fer`)

## Project Conventions

### Error Handling

- **Diagnostics for user errors:** Use `ctx.Diagnostics.Add()` with location info
- **Internal errors:** Use `ctx.ReportError()` for compiler bugs (no location)
- Never panic in parsing/compilation paths (only for assertions in tests)

### Concurrency Patterns

**Parallel Parsing Strategy:**
- Each module spawns its own goroutine (unbounded, simpler than worker pools)
- Module deduplication via `sync.Map` (lock-free for reads)
- `sync.WaitGroup` tracks parsing completion
- Works well for typical project sizes (< 100s of modules)

**Lock Hierarchy** (acquire in this order to prevent deadlocks):
1. `CompilerContext.mu` (RWMutex) - Protects `Modules` map and `DepGraph`
2. `Module.Mu` (Mutex) - Protects individual module fields during updates
3. `DiagnosticBag.mu` (Mutex) - Protects concurrent diagnostic collection
4. `SourceCache.mu` (RWMutex) - Protects cached source file contents

**Usage patterns:**
- `GetModulePhase()` uses `RLock` (read-only access)
- `SetModulePhase()` uses `Lock` (exclusive access)
- Always use deferred unlocks: `defer mu.Unlock()`
- Module parsing completion updates use `Module.Mu.Lock()`

### Naming Conventions

- Exported fields in structs use PascalCase
- Ferret visibility is capitalization-based: Uppercase identifiers are exported/public; lowercase identifiers are private
- Ferret struct fields use dot prefix in declarations (`.x: i32`), and struct literals use `=` (`{ .x = 1 } as Point`)
- Test files follow Go convention: `*_test.go`
- Import paths use forward slashes (converted to OS paths internally)

### File Organization

```
internal/
  compiler/          # Entry point: Compile() function
  pipeline/          # Multi-phase pipeline orchestration
  context_v2/        # Module registry, phase tracking, config
  frontend/
    lexer/           # Regex-based tokenizer (no separate scanner)
    parser/          # Recursive descent, error recovery
    ast/             # AST node definitions
  semantics/
    collector/       # Phase 2: Build symbol tables
    resolver/        # Phase 3: Bind identifiers to declarations
    typechecker/     # Phase 4: Type checking & validation
      inference.go   # Type inference from expressions
      compatibility.go # Type compatibility checking
      typechecker.go # Main type checking logic
    consteval/       # Constant expression evaluation
      value.go       # ConstValue type with big.Int/big.Float support
      evaluator.go   # Expression evaluator for compile-time constants
    controlflow/     # CFG analysis, return path validation
    symbols/         # Symbol kinds and attributes
    table/           # Symbol table (lexical scopes)
  diagnostics/       # Error collection & rendering
  source/            # Position/Location tracking
  types/             # Built-in types (i8, i32, str, etc.)
  utils/             # Helpers (fs, strings, numeric, stack)
```

## Language Features (for test cases)

- **Type inference:** `let x := 42` infers `DEFAULT_INT_TYPE` (typically `i32`)
- **Default float literal type:** untyped floats infer `DEFAULT_FLOAT_TYPE` (typically `f64`)
- **Fixed-size arrays:** `[5]i32` with compile-time bounds checking for constant indices
- **Dynamic arrays:** `[]i32` with auto-growth (no bounds checking) - `arr[5] = x` grows array to size 6
- **Array literals:** `[1, 2, 3]`; empty arrays need a type: `[] as []i32`
- **Negative indexing:** Both array types support `arr[-1]` for reverse access
- **Range expressions:** `start..end[:step]` or `start..=end[:step]`; float steps require float endpoints (`0.0..8.0:1.5`)
- **Optional types:** `i32?` with coalescing operator `value ?? default`
- **Result types:** `Error ! Result` with `catch` blocks
- **Structs:** `type Point struct { .x: f64, .y: f64 };` and literals `{ .x = 1.0, .y = 2.0 } as Point`
- **Enums:** `type Color enum { Red, Green, Blue };`
- **Methods:** `fn (p: Point) distance() -> f64 { ... }`
- **Import restrictions:** Import aliases reserve their names (cannot declare symbols with same name)
- **Printing:** `import "std/io"; io::Println("msg");`

## Common Pitfalls

1. **Don't modify `ctx.Modules` without locking** (`ctx.mu.Lock()`)
2. **Don't assume phase progression** (always check with `GetModulePhase()`)
3. **Import paths ≠ file paths** (use `ImportPathToFilePath()` for resolution)
4. **Include 3+ lines context** when modifying parser error messages (helps locate code)
5. **WASM build uses different entry point** (`main_wasm.go` vs `main.go`)
6. **Import aliases are reserved names** - collector phase checks prevent symbol declarations with import alias names
7. **Array type parsing** - Fixed-size array length is extracted from AST via `extractConstantIndex()` helper

## Semantic Analysis Architecture

### Phase 2: Symbol Collection (`collector/`)
- Traverses AST and declares names (let, const, fn, type)
- Creates scoped symbol tables (module, function, block scopes)
- Validates parameter definitions (variadic rules, duplicates)
- No type resolution yet - symbols get `TypeUnknown` initially
- Builds import alias maps for qualified access
- **Enforces import alias exclusivity:** Prevents declaring symbols with names that conflict with import aliases
- **Cross-module method restriction:** Methods must be defined in the same module as their type

### Phase 3: Resolution (`resolver/`)
- Binds identifiers to their declarations via scope lookup
- Resolves qualified names (`module::symbol`) to imported symbols
- Detects undefined symbols and reports errors
- Validates scope access rules

### Phase 4: Type Checking (`typechecker/`)
- **Type inference** (`inference.go`): Infers types from expressions, includes array type adoption from context
- **Type compatibility** (`compatibility.go`): Checks assignability, conversions
- **Type checking** (`typechecker.go`): Validates declarations, statements, expressions
- **Constant propagation** (`internal/semantics/consteval/`): Compile-time constant evaluation system
  - Tracks constant values through local variables: `let i := 5; i = i + 10; arr[i]`
  - Evaluates arithmetic, comparison, and logical operations on constants
  - Updates constant values on assignment, clears on non-constant assignment
  - Handles `true`/`false` identifiers (parsed as identifiers, not literals)
  - Uses `big.Int` and `big.Float` for arbitrary precision
  - **Scope:** Only tracks primitive local variables, not struct fields or complex expressions
  - **Sound & conservative:** May miss some optimizations but never gives wrong results
- **Array bounds checking:** Compile-time validation for fixed-size arrays with constant indices
  - Fixed-size arrays (`[N]T`): Parse array size from type annotation, validate constant indices
  - Supports negative indices: `-1` to `-N` map to last element to first element
  - Dynamic arrays (`[]T`): No bounds checking - auto-grow on access/assignment instead
  - Uses constant propagation: catches `let i := 10; arr[i]` and `i = i + 5; arr[i]`
  - Error code: `ErrArrayOutOfBounds` (T0009)
- **Dead code detection:** Warns about unreachable code from constant conditions
  - `if true { ... } else { ... }` → warns about dead else branch (W0002)
  - `if false { ... } else { ... }` → warns about dead then branch (W0003)
  - `while false { ... }` → warns loop never executes (W0003)
  - `while true { ... }` → no warning (intentional infinite loops are common)
  - Works with constant expressions: `if 1 + 1 == 2`, `if FLAG` where FLAG is const
- **Control flow** (`controlflow/`): CFG construction, return path analysis, unreachable code detection
  - Only reports obvious infinite loops (no break/return statements)
  - Conservative approach avoids false positives
- Contextual typing for untyped literals
- Optional type wrapping (T → T?)

### Symbol Table Architecture (`table/`)
- Lexical scoping with parent chain (`Parent *SymbolTable`)
- `Lookup(name)` walks parent chain for resolution
- `GetSymbol(name)` checks only current scope
- Each block/function/module has its own scope
- `Module.CurrentScope` tracks active scope during traversal
- `Module.EnterScope(scope)` returns defer-able restore function

## Error Codes Reference

All error codes are defined in `internal/diagnostics/codes.go`:

**Type Errors (T-prefix):**
- **T0003** (`ErrRedeclaredSymbol`): Symbol/import alias redeclaration
- **T0009** (`ErrArrayOutOfBounds`): Array index out of bounds (compile-time)
- **T0010+**: Other type system errors (renumbered to accommodate T0009)

**Warnings (W-prefix):**
- **W0002** (`WarnConstantConditionTrue`): Condition is always true, else branch unreachable
- **W0003** (`WarnConstantConditionFalse`): Condition is always false, then branch/loop unreachable
- **W0004** (`WarnDeadCode`): Dead code detected (currently unused, reserved for future)

Always use error code constants from the `diagnostics` package, never hardcoded strings.

## Key Files to Reference

- `internal/context_v2/context.go`: Architecture comments (lines 1-20), module phase system
- `internal/pipeline/pipeline.go`: Multi-phase pipeline, concurrency patterns
- `internal/semantics/collector/collector.go`: Symbol table construction, import alias validation
- `internal/semantics/resolver/resolver.go`: Name binding and resolution
- `internal/semantics/typechecker/typechecker.go`: Type checking logic, array bounds checking, assignment tracking
- `internal/semantics/typechecker/inference.go`: Type inference, array type adoption
- `internal/semantics/consteval/value.go`: Constant value representation with arbitrary precision
- `internal/semantics/consteval/evaluator.go`: Expression evaluator for compile-time constants
- `internal/semantics/controlflow/cfg.go`: Control flow graph and analysis
- `internal/diagnostics/codes.go`: Central error code definitions (T-prefix errors, W-prefix warnings)
- `internal/diagnostics/emitter.go`: Error rendering with colors
- `internal/frontend/parser/parser.go`: Recursive descent patterns
- `internal/frontend/parser/typ.go`: Type annotation parsing (array types)
- `test_project/`: Example `.fer` files for testing imports/features

## Constant Propagation System

The compiler includes a **sound and conservative** constant propagation system that tracks compile-time known values.

### What is Tracked ✅
- **Primitive local variables**: `let i := 5`, `const MAX := 100`
- **Arithmetic expressions**: `i + 10`, `a * 2`, `x - 5`
- **Comparison operations**: `a < b`, `x == y`, `n > 0`
- **Logical operations**: `true && false`, `!condition`, `a || b`
- **Assignment updates**: `i = i + 10` (tracks new value)
- **Boolean literals**: `true` and `false` (parsed as identifiers)

### What is NOT Tracked ❌
- **Struct fields**: `counter.value` - can be modified via methods with `&` receivers
- **Array/map elements**: Complex aliasing makes tracking unsound
- **Function call results**: May have side effects or non-deterministic results
- **Non-constant assignments**: `i = n` where `n` is a parameter - clears constant value

### Implementation Details

**ConstValue Type** (`consteval/value.go`):
- Uses `*big.Int` and `*big.Float` for arbitrary precision
- Supports Int, Float, Bool, String kinds
- Stored in `Symbol.ConstValue` field (interface{} to avoid import cycles)

**Expression Evaluator** (`consteval/evaluator.go`):
- `EvaluateExpr()`: Main entry point, returns `*ConstValue` or `nil`
- Handles: BasicLit, IdentifierExpr, UnaryExpr, BinaryExpr, ParenExpr
- Special case: `true`/`false` identifiers treated as boolean literals
- Conservative: returns `nil` for anything it can't safely evaluate

**Assignment Tracking** (typechecker.go):
- `checkAssignStmt()` evaluates RHS and updates `Symbol.ConstValue`
- If RHS is constant → store new value
- If RHS is non-constant → clear value (set to `nil`)

### Use Cases

**Array Bounds Checking:**
```ferret
let arr: [10]i32 = [...];
let i := 5;
i = i + 10;       // i is now 15
arr[i];           // Error: index 15 out of bounds
```

**Dead Code Detection:**
```ferret
if true {         // Warning: condition always true
    let x := 1;
} else {
    let y := 2;   // Warning: unreachable
}

while false {     // Warning: condition always false
    let z := 3;   // Warning: loop never executes
}
```

**Constant Propagation:**
```ferret
const FLAG := true;
let condition := FLAG;  // condition is constant true
if condition {          // Detected as always true
    // ...
}
```

### Reference Semantics & Aliasing

Ferret uses `&T` and `&mut T` for reference types (C++-style reference semantics):
- `&` and `&mut` appear in type signatures and expressions: `let r := &value`, `let m := &mut value`
- There is no pointer arithmetic or raw address manipulation
- Method calls with `&Receiver` or `&mut Receiver` modify the original value

**Conservative approach for soundness:**
- Struct fields are **not tracked** as constants
- Methods with `&` receivers can modify fields unpredictably
- Users can copy to local variable if constant tracking needed:
  ```ferret
  let i := counter.value;  // Copy to local - now tracked
  arr[i];                   // Can use constant propagation
  ```

### Testing

Test files demonstrate the system's capabilities:
- `constant_propagation_test.go`: 11 tests for array bounds with constants
- `assignment_propagation_test.go`: 10 tests for tracking through assignments
- `dead_code_test.go`: 16 tests for if/while constant conditions (11 + 5)

Total: **37 tests** covering all aspects of constant propagation.
