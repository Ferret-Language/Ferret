# Language Core Spec v0.1 Draft

This document defines a small, restrictive core. The goal is to keep the type, ownership, and parsing rules predictable.

## 1. Goals

- No GC
- No hidden allocation or clone
- Deterministic destruction
- Safe code prevents use-after-free and data races
- Simple parser rules
- Easy embedding and FFI

## 2. Names and Paths

- The language has no namespace syntax based on `.`
- Imported names, static fields, and enum variants are accessed with `::`
- Examples: `math::complex`, `Color::red`, `Point::origin`

### Import Model

Imports are not relative.

- local imports are package-root-relative
- standard library imports use the reserved `std/` prefix
- dependency imports use a manifest-defined alias as the first path segment

Examples:

```go
import "math/vec2"
import "util/build"
import "std/io"
import "json/parser"
```

Rules:

- `import "../util/build"` is invalid
- `import "./util/build"` is invalid
- source-visible imported name defaults to the last path segment
- `import "util/build"` binds `build`
- `import "math/vec2"` binds `vec2`
- `as` overrides that default binding name
- local source files may not use the reserved top-level name `std`
- local source files may not use a top-level name that is reserved as a dependency alias
- source import paths are stable user syntax, but the compiler should use origin-qualified internal module identities such as `local:math/vec2`, `stdlib:std/io`, and `dependency:json/parser`

Dependency aliases come from the package manifest, not from ad hoc source-level path rewriting.

## 3. Declarations

All named type declarations start with `type`.

```go
type Point struct {
    x i32 = 0
    y i32 = 0

    static origin Point = .{}
}

type Shape interface {
    area() f64
}

type Color enum {
    red
    green
    blue
}

type Token union {
    i64
    string
}

type IoErr error {
    not_found
    denied
    oom
}
```

Anonymous forms are valid anywhere a type is allowed:

```go
struct { x i32, y i32 }
interface { area() f64 }
enum { red, green, blue }
union { i64, string }
(i32, string, bool)
```

Only named types may have methods, and methods are declared outside the type body.

### Visibility

There is no `pub` / `priv` keyword.

- a name starting with an uppercase letter is public
- a name starting with a lowercase letter is private

Examples:

```go
type Point struct {}
type pointCache struct {}

fn Build() i32 {}
fn helper() i32 {}
```

### Conditional Declarations

Top-level declarations may be conditionally included with `#[if(...)]`.

Current supported forms are intentionally small:

```go
#[if(debug)]
#[if(not, debug)]
#[if(target_os, "linux")]
#[if(target_arch, "amd64")]
```

Rules:

- `#[if(...)]` applies to the following top-level declaration
- filtering happens after parsing and before collector/resolver/typechecking
- excluded declarations are not semantically analyzed
- the current implementation does not support `&&`, `||`, or arbitrary expressions

### Variables And Constants

Local variables and constants use these forms:

```go
let mut a: i32 = 1
let a = 2
let mut tmp
let value
const BuildMode = "debug"
```

Rules:

- `let mut` declares a mutable variable
- `let` declares an immutable variable
- `const` declares a compile-time constant
- `const` occurrences are intended to be replaced during compile-time evaluation
- whether uninitialized `let` is legal depends on definite-assignment rules in analysis

### Comptime

Compile-time participation is explicit in syntax.

Examples:

```go
fn add(comptime T Type, x T) T

let a = comptime 1 + 2
```

Rules:

- `comptime` may mark parameters
- `comptime` may prefix expressions
- exact compile-time evaluation boundaries are enforced by later analysis

### Constructors And Destructors

Named types use constructor and destructor names in the C++ style.

- constructor name matches the type name
- destructor name is `~Type`
- static methods are not part of the language

Examples:

```go
fn (p *mut Point) Point(x i32, y i32) {}
fn (p *mut Point) ~Point() {}
```

Parser rule:

- a function with a receiver is a constructor if its name matches the receiver base type
- a function with a receiver is a destructor if it is spelled `~Type`

## 4. Type Forms

The core type forms are:

- Scalars: `bool`, integers, floats, `char`
- Tuples: `(T1, T2, ..., Tn)`
- Arrays: `[N]T`
- Read pointers: `*T`
- Mutable pointers: `*mut T`
- Owning pointers: `*own T`
- Raw pointers: `*raw T`, `*raw mut T`
- Optional: `?T`
- Error union: `E!T`
- Structs, enums, unions, interfaces

Interpretation:

- `*T` is a non-owning non-null read pointer
- `*mut T` is a non-owning non-null mutable pointer
- `*own T` is the unique non-null owner of one heap allocation
- `*raw T` and `*raw mut T` are unchecked pointers and require `unsafe` to dereference
- `?T` is the only way to represent absence
- `E!T` is a value-or-error type where `E` is a named `error` type

`none` is only valid for `?T`.
`?*T`, `?*mut T`, and `?*own T` are the nullable forms of safe pointers.

## 5. Copy vs Move

The language does not use a type-level `copy` keyword.

Default behavior:

- Scalars are copied
- Enums are copied
- Non-owning pointers are copied
- `*own T` is moved
- Structs, unions, interfaces, tuples, arrays, `?T`, and `E!T` are moved by default

Assignment rules:

- assigning a copy type copies
- assigning a move type moves
- deep copy is explicit with the `copy` expression

Current standard behaviour:

- copy types:
  - builtin scalars
  - enum values
  - error members / error-set values
  - non-owning pointers
- move types:
  - `str`
  - arrays and slices
  - structs, unions, interfaces
  - `*own T`
  - user-defined resource types
- `copy expr` is the only explicit duplication form in safe code
- a named type may be explicitly marked `move` to force move-only behaviour
  even if its underlying representation would otherwise be copyable

Examples:

```go
type File move struct {
    fd i32
}

type Handle move enum {
    stdin
    stdout
}
```

```go
let a: i32 = 1
let b = a

let p: Point = .{ .x = 1, .y = 2 }
let q = copy p

let f: *own File = new(a, .{ .fd = 3 })!!
let g = f
```

`copy expr` means “explicitly duplicate this value”.

- it is valid only for values whose type supports copying

## 6. Explicit Casts

Value conversion is explicit.

```go
let small = big as i8
let wide = small as i64
```

Rules:

- `expr as T` is the explicit conversion form
- implicit widening is allowed where it is lossless
- narrowing or cross-family conversion requires `as`
- `as` is for value conversion, not unchecked bit reinterpretation
- unchecked reinterpretation must stay inside `unsafe`
- it may be trivial for scalars
- it may perform a deep copy for aggregates
- if a type cannot be duplicated safely, `copy expr` is a compile-time error

## 6. Ownership and Lifetime

Objects have lifetimes. Pointers do not own memory unless marked `own`.

- Stack values are owned by lexical scope
- Heap values are owned by exactly one `*own T`
- When an `*own T` binding leaves scope, its allocation is destroyed and freed unless ownership was moved away
- `*T` and `*mut T` never free memory

The language does not permit shared ownership in safe code.

## 7. Borrowing

Borrowing uses ordinary pointer syntax:

```go
let x: i32 = 10
let p: *i32 = &x
let m: *mut i32 = &mut x
```

Borrowing from an owner is explicit:

```go
let h: *own Point = new(a, .{ .x = 1, .y = 2 })!!
let p: *Point = &*h
let m: *mut Point = &mut *h
```

Safe-code borrow rules:

- A borrow is lexical
- A borrow may not escape its block
- A borrow may not be returned
- A borrow may not be stored in heap memory, globals, statics, or closures
- A borrow may not be captured by `defer`
- A borrow may not cross thread boundaries
- While a borrow of an owner is live, the owner is frozen

Frozen means:

- it may not be moved with `take`
- it may not be freed
- it may not be reallocated with `reserve`
- it may not be sent to another thread

This rule is block-scoped rather than last-use analyzed.

## 8. Heap Allocation

Heap allocation is explicit and always uses an allocator.

```go
fn new[T](a Alloc, value T) Oom!*own T
fn alloc[T](a Alloc, count usize) Oom!*own T
fn free[T](take p *own T)
fn reserve[T](take p *own T, old_count usize, new_count usize, a Alloc) Oom!*own T
```

Semantics:

- `new` allocates one initialized `T`
- `alloc` allocates contiguous storage for `count` `T` values; contents are `undefined`
- `free` destroys the pointee(s) and frees the allocation
- `reserve` consumes the old owner and returns a resized contiguous allocation; the returned address may differ

`reserve` may not be called while any borrow of the allocation is live.

## 9. `undefined`

`undefined` means allocated but not initialized.

Allowed:

- local variables of plain-old-data types with trivial storage
- `alloc`
- `new(a, undefined)` for plain-old-data types with trivial storage

Forbidden:

- `?T`
- `E!T`
- `union`
- `interface`
- any type with `_drop` or `_clone`
- any type containing owning pointers

Reading `undefined` before initialization is invalid.

## 10. Composite Literals

The language uses Zig-style composite literal notation.

- `.{ ... }` is a composite literal
- bare `{ ... }` is never a value literal
- `.{ ... }` is only valid when the expected type is already known
- if the expected type is missing or ambiguous, `.{ ... }` is a compile error

Examples:

```go
let p: Point = .{ .x = 1, .y = 2 }
let q: Point = .{}
return .{ .x = 3, .y = 4 }
draw(.{ .x = 1, .y = 2 })
if p == .{ .x = 1, .y = 2 } {
}
```

Named field form:

```go
.{ .x = 1, .y = 2 }
```

Positional form:

```go
.{ 1, 2, 3 }
```

Mixing named and positional elements in the same literal is invalid.

If no contextual type exists, the programmer must provide one through the surrounding expression:

```go
let p: Point = .{ .x = 1, .y = 2 }
```

## 11. Optional

Optional is built in.

- `?T` means either `T` or `none`
- no other type may hold `none`
- `??` unwraps with fallback

Examples:

```go
let a: ?i32 = none
let b: ?i32 = 10
let c: i32 = b ?? 0
```

## 12. Errors

Errors are named sets declared with `error`.

```go
type IoErr error {
    not_found
    denied
    oom
}
```

Error union syntax:

```go
IoErr!T
```

Handling:

- `expr!!` propagates the error from the current function
- `expr catch fallback` handles locally
- explicit branching is also valid

Examples:

```go
let file = open(path, a)!!
let port = parse_port(text) catch 8080
```

Ignoring an `E!T` value is a compile error unless explicitly discarded.

## 13. Structs

Struct fields may have defaults.

```go
type Point struct {
    x i32 = 0
    y i32 = 0
}
```

Construction:

```go
let p: Point = .{}
let q: Point = .{ .x = 3 }
```

All non-defaulted fields must be initialized.

## 14. Methods and Static Members

Static fields are declared inside the type body. Methods are declared at top level with a receiver, like Go.

```go
type Point struct {
    x i32 = 0
    y i32 = 0

    static origin Point = .{}
}

fn (p Point) len2() i32 {
    return p.x * p.x + p.y * p.y
}

fn (p *mut Point) shift(dx i32, dy i32) {
    p.x += dx
    p.y += dy
}
```

Rules:

- static field initializers must be compile-time constants in v0.1
- mutable static fields are `unsafe`
- static methods do not exist
- receiver forms are:
- `fn (p T) name(...)` for by-value receiver
- `fn (p *T) name(...)` for read borrow receiver
- `fn (p *mut T) name(...)` for mutable borrow receiver
- `fn (p *own T) name(...)` for owning receiver
- there is no implicit `self`
- method call syntax `x.name(...)` is sugar for passing the receiver as the first argument
- when the receiver type is `*T` or `*mut T`, one level of auto-borrow is allowed for the duration of the call only

## 15. Special Methods and Operators

Named types may define special methods for operators.

Supported names in v0.1:

- `_add`
- `_sub`
- `_mul`
- `_div`
- `_eq`
- `_clone`
- `_drop`

Example:

```go
type Point struct {
    x i32
    y i32
}

fn (p Point) _add(other Point) Point {
    return .{ .x = p.x + other.x, .y = p.y + other.y }
}
```

Rules:

- `a + b` lowers to `a._add(b)` when the left operand's named type defines `_add`
- assignment never calls `_clone`
- `take` never calls `_clone`
- `_clone` is only used by explicit `clone(x)`
- `_drop` runs on scope exit and `free`

## 16. Union

`union` is a safe tagged union by member type.

```go
type Token union {
    i64
    string
}
```

Rules:

- member types must be unique
- construction may be implicit when the target type is known
- `switch` over a union must be exhaustive

Example:

```go
let t: Token = 123

switch t {
case i64:
    handle_int(t)
case string:
    handle_string(t)
}
```

If raw overlay behavior is needed for FFI, it is spelled `raw union` and is `unsafe`.

## 17. Interface

Interfaces are structural and Go-like.

```go
type Shape interface {
    area() f64
}
```

Rules:

- a named type satisfies an interface if it defines all required methods
- only named types may satisfy interfaces
- anonymous types cannot satisfy interfaces
- an interface value is a fat pointer to an existing value plus method table
- interface conversion never allocates in v0.1
- interface values are non-owning and follow borrow escape rules

Example:

```go
fn draw(s Shape) {
    print(s.area())
}

let p: Point = .{ .x = 1, .y = 2 }
draw(&p)
```

## 18. Tuples

Tuples are ordered fixed-size value types.

```go
let t: (i32, string, bool) = .{ 1, "ok", true }
```

Tuples are move values by default. `copy t` is the explicit duplication form.

## 19. Control Flow

The core control flow features are:

- `if`
- `switch`
- `for`
- `defer`
- `panic`
- `catch`

Rules:

- `for` uses Zig-style iteration only:
  - `for xs |v| { ... }`
  - `for xs |i, v| { ... }`
- `defer` runs on scope exit in last-in-first-out order
- `defer` is scope-bound, not function-bound
- `defer` only accepts a function call: `defer close(file)`
- `defer` runs on normal return and panic unwind
- `panic` is a statement keyword with mandatory payload: `panic "bad"`
- `panic("bad")` is invalid
- `recover` is reserved for future unwind handling and is not part of the current surface syntax
- `expr catch fallback` yields the fallback value
- `expr catch |err| { ... }` is valid only when the handler exits on all paths

## 20. Concurrency

The safe concurrency model is ownership transfer plus restricted shared state.

Rules:

- `spawn` may copy copy-by-default values and move move-by-default values
- `send` is a built-in marker property derived structurally for types whose ownership may cross threads
- borrows may not cross thread boundaries
- `*own T` may cross thread boundaries only if `T` is `send`
- shared mutable state is only through `mutex T` and `atomic T`

Mutex locking syntax:

```go
lock m as g {
    use(g)
}
```

Semantics:

- `g` is a scoped mutable borrow of the protected value
- `g` cannot escape the `lock` block
- safe code may hold at most one mutex guard at a time
- nested `lock` is a compile error in safe code

The language does not claim general compile-time prevention of channel deadlocks.

## 21. Compile-time Guarantees in Safe Code

Safe code guarantees:

- no use-after-free
- no double-free
- no null dereference from `*T` or `*mut T`
- no `none` unless the type is `?T`
- no ignored error union without explicit discard
- no data races
- no nested-lock deadlock
- no read of provably uninitialized data
- exhaustive `switch` on `enum` and `union`

These guarantees exclude `unsafe`, `*raw T`, FFI, and explicit unchecked casts.
