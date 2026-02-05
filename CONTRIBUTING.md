
# Ferret Compiler Project Contributor Instructions

## Project Overview
Ferret is a statically typed, modern programming language designed for clarity, simplicity, and expressiveness. This compiler is written in Go and focuses on catching errors at compile time while providing rich error diagnostics and fast compilation speeds.

## Core Design Philosophy

### 1. Architecture Principles
- **Maintainability First**: Design for easy extension and modification by new developers
- **Clean Separation**: Clear boundaries between compilation phases (frontend → semantic → backend)
- **Modular Design**: Each component should be independently testable and replaceable
- **Developer-Friendly**: New contributors should be able to understand and extend features with ease
- **Shared Context**: Use `CompilerContext` to pass state through all compilation phases

### 2. Compilation Pipeline
The compiler follows a multi-stage pipeline:
```
Source Code → Lexer → Parser → AST → Semantic Analysis → HIR Gen → HIR CFG/Analysis → HIR Lowering → MIR Gen
```

**Current Architecture (`internal/`):**

**Frontend (`internal/frontend/`)**
- `lexer/`: Tokenization and lexical analysis (Complete)
- `parser/`: Syntax parsing and AST generation (Complete)
- `ast/`: Abstract syntax tree node definitions (Complete)

**Supporting Systems**
- `context_v2/`: Compiler context and state management (Complete)
- `diagnostics/`: Error reporting and diagnostics system (Complete)
- `source/`: Source file management and position tracking (Complete)
- `types/`: Built-in type definitions (Complete)
- `utils/`: Utility functions and helpers (Complete)

**Semantic Analysis (`internal/semantics/`)**
- Symbol table management (Implemented, evolving)
- Resolution and scope validation (Implemented, evolving)
- Type checking and inference (Implemented, evolving)
- Const evaluation (Implemented, limited)
- HIR control-flow analysis (Implemented)

**HIR Pipeline**
- `hir/`: HIR node definitions
- `hirgen/`: AST → HIR generation
- `hirlower/`: HIR canonicalization/lowering for later stages

**MIR Pipeline**
- `mir/`: MIR node definitions
- `mirgen/`: HIR → MIR generation (scaffold)

**Pipeline**
- `pipeline/`: Orchestrates phases across modules

**Backend**
- `codegen/`: C code generation (AST-based, present but detached from pipeline)
- MIR-native/WASM backends (In Progress)
- Optimization passes (Planned)

### 3. Error Handling Strategy
- **Comprehensive Collection**: Collect ALL errors, warnings, and diagnostics via `CompilerContext.Diagnostics`
- **Smart Recovery**: Parser implements error recovery to continue parsing after errors
- **Rich Diagnostics**: Provide clear, actionable error messages with source location
- **Severity Levels**: Support for Error, Warning, Info, and Hint diagnostics
- **Structured Reporting**: Use error codes (e.g., `P0001`, `L0007`, `S0001`) for categorization

### 4. Type System Design
- **Static Typing**: Strong type checking at compile time
- **Type Inference**: `:=` operator for automatic type deduction
- **Rich Type Features**:
  - Basic types: `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`, `f32`, `f64`, `str`, `bool`, `byte`
  - Optional types: `?T` for nullable values
  - Result types: `E ! T` for error handling
  - Reference types: `&T` for references (parsed, semantics pending)
  - Arrays: `[]T` (dynamic), `[N]T` (fixed-size)
  - Maps: `map[K]V`
  - Structs, interfaces, enums
- **Advanced Features**: Variadic parameters (`...T`), method receivers

## Development Guidelines

### Language Feature Priorities

**Completed Features**
- Variables and constants (`let`, `const`)
- Type inference (`:=` operator)
- All basic types and type modifiers
- Structs with methods
- Interfaces
- Enums
- Arrays (fixed and dynamic)
- Maps
- Functions (regular and anonymous)
- Control flow (`if`, `else`, `for`, `while`, `match`)
- Error handling (`E ! T`, `catch` expressions)
- Optional types (`?T`, coalescing operator `??`)
- Scope resolution (`::` for enums and modules)
- Composite literals (structs, arrays, maps)
- Variadic parameters (`...T`)

**🚧 In Progress**
- MIR lowering and backend integration
- MIR-native/WASM backends
- Optimization passes

**🔮 Future Features**
- Generics
- MIR-based backends
- Advanced optimizations
- Multiple backend targets
- `defer` statements
- `fork` for coroutines

### Testing Standards
- **Unit Tests Primary**: Focus on comprehensive unit testing
- **Test Coverage**: Each package should have corresponding `*_test.go` files
- **Test Files**: Create `.fer` test files in `tests/` directory
- **AST Validation**: Compiler outputs `.ast.json` files for verification
- **Table-Driven Tests**: Use table-driven tests where appropriate
- **Integration Tests**: For end-to-end compilation workflows
- **Error Testing**: Verify error messages and recovery behavior

### Performance Goals
- **Fast Compilation**: Prioritize compilation speed over complex optimizations
- **Compile-Time Safety**: Catch errors at compile time, minimize runtime errors
- **Memory Efficiency**: Efficient AST and symbol table representations

### Module System Design
- **No config required**: Ferret projects don't require a `fer.ret` file
- **Simple imports**: `import "module/path"`
- **Local imports**: Relative path-based
- **Remote dependencies**: Future feature (planned GitHub integration)
- **Clean builds**: AST files (`.ast.json`) generated alongside source

## Coding Standards

### Error Handling
```go
// Use the diagnostics system through CompilerContext
if err != nil {
    ctx.Diagnostics.Report(diagnostics.Diagnostic{
        Severity: diagnostics.Error,
        Code:     "P0001",
        Message:  "descriptive error message",
        Location: location,
    })
    return // Parser continues with error recovery
}

// Only panic for programming errors or invalid states
if ctx == nil {
    panic("Cannot create parser: CompilerContext is nil")
}
```

### Context Management
```go
// Always pass CompilerContext through the pipeline
func NewParser(ctx *context.CompilerContext, filePath string) *Parser {
    // Validate inputs
    if ctx == nil {
        panic("CompilerContext cannot be nil")
    }
    if filePath == "" {
        panic("File path cannot be empty")
    }
    // Use context for diagnostics, source management, etc.
    return &Parser{
        ctx:      ctx,
        filePath: filePath,
        // ...
    }
}
```

### AST Node Design
```go
// All AST nodes implement the Node interface
type Node interface {
    INode()                      // Marker method
    Loc() *source.Location       // Source location
}

// Different node categories
type Expression interface {
    Node
    Expr()  // Marker for expressions
}

type Statement interface {
    Node
    Stmt()  // Marker for statements
}

type TypeNode interface {
    Node
    TypeExpr()  // Marker for type expressions
}

// Example AST node with embedded location
type BinaryExpr struct {
    X  Expression
    Op lexer.Token
    Y  Expression
    source.Location
}

func (b *BinaryExpr) INode() {}
func (b *BinaryExpr) Expr() {}
func (b *BinaryExpr) Loc() *source.Location { return &b.Location }
```

### Testing Patterns
```go
func TestParserFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected interface{}
        wantErr  bool
    }{
        // Test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

### Ferret Language Syntax

#### Variable and Constant Declaration
```ferret
let x := 10;              // Type inference with walrus operator
let y: i32 = 20;          // Explicit type
const pi := 3.14;         // Constant with inference
const name: str = "Ferret"; // Constant with explicit type
```

#### Basic Types
```ferret
// Integers
i8, i16, i32, i64         // Signed integers
u8, u16, u32, u64         // Unsigned integers

// Floating point
f32, f64

// Other
str                       // String
bool                      // Boolean (true/false)
byte                      // Single byte
none                      // Null/nil value
```

#### Arrays
```ferret
let arr: []i32 = []i32{1, 2, 3};  // Dynamic array
let fixed: [3]i32 = [3]i32{1, 2, 3}; // Fixed-size array
let empty := [];                   // Empty array (type inferred from usage)
```

#### Maps
```ferret
type MyMap map[str]i32;

let scores: map[str]i32 = map[str]i32{
    "Alice" => 95,
    "Bob" => 87
};
```

#### Structs
```ferret
// Struct type definition
type Point struct {
    .x: f64,
    .y: f64
};

// Struct instantiation
let p := Point{
    .x = 1.0,
    .y = 2.0
};

// Field access
let x_val := p.x;

// Nested structs
type Line struct {
    .start: Point,
    .end: Point
};

// Chained field access
let start_x := line.start.x;
```

#### Enums
```ferret
type Color enum {
    Red,
    Green,
    Blue
};

// Enum usage with scope resolution operator
let color := Color::Red;

// Pattern matching with match
match color {
    Color::Red => println("Red"),
    Color::Green => println("Green"),
    Color::Blue => println("Blue")
}
```

#### Interfaces
```ferret
type Shape interface {
    fn area() -> f64,
    fn perimeter() -> f64
};

// Implement interface for a struct
type Circle struct {
    .radius: f64
};

fn (c: Circle) area() -> f64 {
    return 3.14 * c.radius * c.radius;
}

fn (c: Circle) perimeter() -> f64 {
    return 2.0 * 3.14 * c.radius;
}
```

#### Functions
```ferret
// Regular function
fn add(a: i32, b: i32) -> i32 {
    return a + b;
}

// Method with receiver
fn (p: Point) distance() -> f64 {
    return 0.0; // Calculate distance from origin
}

// Variadic parameters
fn sum(nums: ...i32) -> i32 {
    let total := 0;
    return total;
}

// Anonymous function
let multiply := fn (a: i32, b: i32) -> i32 {
    return a * b;
};
```

#### Optional Types
```ferret
let maybe: ?i32 = none;
let value := maybe ?? 42;  // Coalescing operator - defaults to 42 if none

// Optional chaining
let result: ?str = getValue()?;
```

#### Error Types and Handling
```ferret
// Function returning error
fn divide(a: i32, b: i32) -> str ! i32 {
    if b == 0 {
        return "division by zero"!;
    }
    return a / b;
}

// Custom error type
type MyError struct {
    .code: i32,
    .msg: str
};

fn process(data: str) -> bool ! MyError {
    if data == "" {
        return error: MyError{ .code = 1, .msg = "empty data" };
    }
    return true;
}

// Catch expressions - three forms:

// 1. Catch with handler block and fallback
let result := div(10, 0) catch err {
    println(err);
} 0;

// 2. Catch with handler block only
let x := div(10, 2) catch err {
    return 0;
};

// 3. Catch with fallback only
let y := div(10, 2) catch 0;
```

#### Control Flow
```ferret
// If-else
if x < y {
    println("x is less");
} else if x > y {
    println("x is greater");
} else {
    println("equal");
}

// For loop
for i := 0; i < 10; i++ {
    println(i);
}

// While loop
while condition {
    // loop body
}

// Pattern matching (match)
match value {
    1 => println("one"),
    2 => println("two"),
    _ => println("other")
}
```

#### Type Casting
```ferret
let x: i32 = 42;
let y := x as f64;  // Cast to float
```

#### Imports
```ferret
import "std/io";
import "mypackage/utils";
```

## Development Workflow

### Before Starting Work
1. Build the compiler: `.\scripts\build.bat` (Windows) or `./scripts/build.sh` (Unix)
2. Run existing tests to ensure current functionality works
3. Check existing test files in `tests/` for similar functionality
4. Consider impact on compilation pipeline stages

### During Development
1. Write tests in `tests/` directory with `.fer` extension
2. Examine generated `.ast.json` files to verify AST structure
3. Use the compiler: `ferret filename.fer` (after building)
4. Run Go tests: `go test ./...`
5. Check for errors using the built-in diagnostics system

### Code Review Standards
- All new features must have tests (`.fer` files in `tests/`)
- Error messages should be clear and actionable
- Use structured diagnostics with proper error codes
- Consider impact on compilation performance
- Maintain backward compatibility in AST structures
- Follow Go coding conventions
- Document any new compiler phases or major changes
- Update relevant documentation (README.md, syntax.md, etc.)

### Building and Testing

**To build the compiler:**
```bash
cd scripts
./build.sh        # Unix/Linux/Mac
.\build.bat       # Windows (PowerShell)
```

This creates the `ferret.exe` (or `ferret`) executable in the `bin/` directory.

**To compile Ferret code:**
```bash
ferret test.fer              # Compile a Ferret file
ferret tests/showcase.fer    # Compile with path
```

The compiler will:
- Parse the source file
- Generate AST and output `filename.fer.ast.json`
- Report any errors with rich diagnostics

**To run Go tests:**
```bash
go test ./...                # Run all tests
go test ./internal/frontend/parser  # Test specific package
go test -v ./...             # Verbose output
```

### Available Scripts

All scripts are in the `scripts/` directory:

```bash
# Build the compiler
./build.sh          # Unix/Linux/Mac
.\build.bat         # Windows

# Run quick example
./ex.bat            # Windows - runs example.fer

# Format Go code
./fmt.sh            # Unix/Linux/Mac
.\fmt.bat           # Windows

# Run tests
go test ./...       # Run all Go tests
```

**Note:** Add `bin/` to your PATH to run `ferret` from anywhere.

## Common Patterns

### Adding New Language Features

**Parser Level:**
1. Update lexer for new tokens (if needed) in `internal/frontend/lexer/`
2. Extend parser for new syntax in `internal/frontend/parser/`
3. Add AST node types in `internal/frontend/ast/`
4. Add test `.fer` files in `tests/`
5. Verify generated `.ast.json` output

**Example: Adding a new expression type**
```go
// 1. Add AST node in internal/frontend/ast/expr.go
type MyExpr struct {
    Field Expression
    source.Location
}

func (m *MyExpr) INode() {}
func (m *MyExpr) Expr() {}
func (m *MyExpr) Loc() *source.Location { return &m.Location }

// 2. Add parsing logic in internal/frontend/parser/parser.go
func (p *Parser) parseMyExpr() *ast.MyExpr {
    start := p.peek().Start
    // parsing logic...
    return &ast.MyExpr{
        Field: expr,
        Location: p.makeLocation(start),
    }
}

// 3. Create test in tests/test_myfeature.fer
fn test() {
    // test code using new feature
}
```

**Semantic Level:**
1. Update symbol collection
2. Update resolution rules
3. Update type checking rules
4. Add semantic validation
5. Comprehensive error cases

### Extending Error Reporting
```go
// Error codes follow pattern: [Category][Number]
// L = Lexer, P = Parser, S = Semantic, T = Type

// In your parser/lexer code:
p.ctx.Diagnostics.Report(diagnostics.Diagnostic{
    Severity: diagnostics.Error,
    Code:     "P0001",  // Parser error code
    Message:  fmt.Sprintf("unexpected token: %s", token.Value),
    Location: p.makeLocation(token.Start),
    Note:     "additional context here",
    Help:     "suggestion to fix the error",
})

// Severity levels:
// - Error: Compilation cannot continue
// - Warning: Potential issue but compilation can proceed
// - Info: Informational message (e.g., trailing comma)
// - Hint: Suggestion for improvement
```

### Working with Symbol Tables
```go
// Declare symbols during collection phase
symbol := &Symbol{
    Name: identifier,
    Type: symbolType,
    Kind: SymbolVar, // or SymbolFunc, SymbolType, etc.
    Node: astNode,
}
ctx.symbol_table.Declare(symbol)

// Resolve symbols during resolution phase
resolved := ctx.symbol_table.Lookup(identifier)
if resolved == nil {
    ctx.Diagnostics.Report(diagnostics.Diagnostic{
        Severity: diagnostics.Error,
        Code:     "S0001",
        Message:  fmt.Sprintf("undefined identifier: %s", identifier),
        Location: location,
    })
}
```

## Project Structure

```
Ferret/
├── bin/                    # Compiled binaries
│   └── ferret.exe
├── colors/                 # Terminal color output utilities
├── internal/               # Internal compiler packages
│   ├── context_v2/        # Compiler context and state
│   ├── codegen/           # Code generation (C backend)
│   ├── diagnostics/       # Error reporting system
│   ├── frontend/          # Frontend (lexer, parser, AST)
│   │   ├── ast/          # AST node definitions
│   │   ├── lexer/        # Tokenization
│   │   └── parser/       # Syntax parsing
│   ├── hir/              # HIR node definitions
│   ├── hirgen/           # AST → HIR lowering
│   ├── hirlower/         # HIR canonicalization/lowering
│   ├── mir/              # MIR node definitions
│   ├── mirgen/           # HIR → MIR generation
│   ├── pipeline/         # Compilation pipeline
│   ├── semantics/        # Semantic analysis (implemented, evolving)
│   ├── source/           # Source file management
│   ├── types/            # Type system definitions
│   └── utils/            # Utility functions
├── scripts/               # Build and utility scripts
├── tests/                 # Test Ferret source files
├── toml/                  # TOML parsing (config files)
├── main.go               # Compiler entry point
├── go.mod                # Go module definition
├── README.md             # Project documentation
├── syntax.md             # Language syntax reference
└── CONTRIBUTING.md       # This file
```

## Current Implementation Status

### Fully Implemented (Frontend)
- **Lexer**: Complete tokenization with all operators and keywords
- **Parser**: Full syntax parsing with error recovery
- **AST**: Complete node definitions for all language features
- **Diagnostics**: Rich error reporting with codes, locations, notes, and hints
- **Context**: Shared compiler context for state management
- **Type Parsing**: All type expressions (basic, optional, error, reference, arrays, maps, structs, interfaces, enums)
- **Expressions**: All expression types including composite literals, method calls, error handling
- **Statements**: All statement types (declarations, control flow, assignments)
- **Error Handling**: `E ! T` syntax with `catch` expressions
- **Advanced Features**: Variadic parameters, method receivers, scope resolution

### ✅ Implemented (Semantic Analysis)
- Symbol collection and resolution
- Type checking and inference
- Semantic validation
- Const evaluation (limited)
- HIR control-flow analysis

### 🚧 In Progress (Backend)
- C code generation completeness
- HIR → MIR lowering
- MIR backends (native/WASM)

### 🔮 Planned (Backend)
- Optimization passes
- Multiple backend targets
- Runtime system

## Contributing Checklist

Before submitting a PR:
- [ ] Code builds successfully (`.\scripts\build.bat`)
- [ ] All Go tests pass (`go test ./...`)
- [ ] Added `.fer` test files for new features
- [ ] Verified `.ast.json` output is correct
- [ ] Error messages are clear and helpful
- [ ] Code follows Go conventions
- [ ] Updated documentation if needed
- [ ] No breaking changes to existing AST structure

## Getting Help

- Check existing test files in `tests/` for examples
- Review `syntax.md` for language syntax reference
- Examine AST definitions in `internal/frontend/ast/`
- Look at parser implementation in `internal/frontend/parser/`
- Read diagnostic codes in error messages for debugging

---

**Note:** This document will be updated as the compiler evolves and new patterns emerge. Last updated: November 2025
