# Ferret Current Semantics

This document describes the **current compiler behavior** in `Ferret-compiler-v2/compiler`.

It is not a future design RFC. It is a discussion snapshot of what the language currently means and what the compiler currently enforces.

Where behavior is intentionally incomplete or still unsettled, that is called out explicitly.

---

## 1. Core Model

Ferret currently uses these semantic categories:

- `T` = ordinary value type
- `*T` = owning heap pointer
- `&T` = immutable borrowed reference
- `&mut T` = mutable borrowed reference
- `^T` = raw pointer

Current design intent:

- plain values are copyable by default
- owning pointers are move-only by default
- references are non-owning
- raw pointers are unsafe

There are no special constructor/destructor language concepts anymore. Allocation and cleanup are intended to be explicit.

---

## 2. Visibility

Visibility is currently name-based.

- uppercase first letter = public
- lowercase first letter = private outside the defining module

This applies to:

- types
- functions
- methods
- module symbols
- struct fields

Struct field privacy is currently **module-level**, not type-level.

That means:

- code in the same module can access lowercase fields
- code in other modules cannot

---

## 3. Primitive And Builtin Types

Current builtin scalar/value types include:

- `bool`
- `char`
- signed integers: `i8`, `i16`, `i32`, `i64`, `isize`
- unsigned integers: `u8`, `u16`, `u32`, `u64`, `usize`
- floats: `f32`, `f64`
- `void`
- `str`

Other builtin type forms:

- arrays: `[N]T`
- inferred-length arrays: `[_]T`
- slices: `[]T`
- tuples: `(T1, T2, ...)`
- optional: `?T`
- error union: `E!T`

Current status notes:

- tuples exist in the frontend/type system but are not a fully settled surface
- slices exist semantically and in backend support paths, but slice literal construction is not finalized
- `str` exists and works, but its final semantic design is still under discussion

---

## 4. Structs, Enums, Unions, Interfaces

### Struct

```ferret
type Point struct {
    X: i32
    y: i32 = 0
}
```

Rules:

- fields use `name: Type`
- field defaults are supported
- uppercase/lowercase controls module-level visibility
- static struct fields are no longer part of the language

### Enum

```ferret
type Color enum {
    Red,
    Green,
    Blue,
}
```

Enum values are copyable by default.

### Union

```ferret
type Token union {
    i32,
    str,
}
```

Named unions exist and are part of the type system and IR.

### Interface

```ferret
type Shape interface {
    Draw(&self)
    MoveBy(&mut self, dx: i32, dy: i32)
    New() Self
}
```

Rules:

- interface methods include receiver form in the parameter list
- receiver form participates in interface matching
- static interface methods are allowed by omitting a receiver parameter
- `Self` is supported in interface signatures and is instantiated to the concrete implementing type during matching

---

## 5. Type Annotations

Current syntax consistently uses `:`

Examples:

```ferret
let x: i32 = 10

fn add(x: i32, y: i32) i32 {
    return x + y
}

type Point struct {
    X: i32
}
```

Current rule:

- `let` bindings use `name: Type`
- parameters use `name: Type`
- struct fields use `name: Type`

---

## 6. Value Semantics

### Plain Values

`T` means an ordinary value.

Examples:

- `i32`
- `bool`
- `Point`
- `[3]i32`

Current behavior:

- plain values are copyable by default
- assignment copies them
- passing them by plain `T` parameter passes by value

### Owning Pointer

`*T` is an owning heap pointer.

Current intent/behavior:

- move-only by default
- used for owned heap storage
- dereferenced with `*`
- method receiver form `*self` means consuming/owning receiver semantics

### Immutable Reference

`&T` is an immutable borrowed reference.

Current behavior:

- non-owning
- stack-only
- cannot escape to module scope
- cannot be stored in heap-owned values
- multiple immutable borrows are allowed as long as no mutable borrow conflicts

### Mutable Reference

`&mut T` is a mutable borrowed reference.

Current behavior:

- non-owning
- exclusive while live
- blocks overlapping immutable/mutable borrows of the same root
- mutable writes go through `*ref`
- direct use of the original value while a live mutable borrow is still active is rejected

### Raw Pointer

`^T` is a raw pointer.

Current behavior:

- unsafe
- created explicitly with raw-address operators:
  - `@expr`
  - `@mut expr`
- intended for FFI / low-level pointer work

---

## 7. Borrow And Address Syntax

Current expression syntax:

- `&x` -> `&T`
- `&mut x` -> `&mut T`
- `@x` -> `^T` in `unsafe`
- `@mut x` -> `^T` in `unsafe`
- `*p` -> dereference

Important distinction:

- `&` / `&mut` are borrow operators
- `@` / `@mut` are raw address operators
- `*` is dereference

This is intended to keep borrow and raw-address concepts separate.

---

## 8. Copy And Move

Current compiler behavior:

- plain values are copyable
- owning pointers `*T` are move-only
- old move-marked type syntax has been removed

### `copy`

`copy` is currently **not implemented**.

Current compiler behavior:

- `copy expr` hard-errors with a “not yet implemented” diagnostic

### `take`

Old `take` support has been removed.

---

## 9. Mutability

Mutability currently appears in two places:

### Binding Mutability

```ferret
let x: i32 = 10
let mut y: i32 = 20
```

Rules:

- `let mut` allows rebinding / mutable access derivation
- `let` does not

### Parameter Mutability

```ferret
fn bump(mut x: i32) void {
    x = x + 1
}
```

Rules:

- parameters may be declared `mut`
- calling a function with a `mut` parameter currently requires a mutable argument binding

### Reference Mutability

`&mut T` means the reference can mutate the pointee.

Important distinction:

- `mut y: &mut T` means the **binding `y`** can be rebound
- `y: &mut T` already means the **pointee** can be mutated through `*y`

So:

- `*y = 12` is valid for `y: &mut i32`
- `y = ...` is only valid if the binding itself is mutable

---

## 10. Methods

Current method declaration syntax is attached-method style:

```ferret
fn Point::Len(&self) i32 {
    return self.X
}

fn Point::MoveBy(&mut self, dx: i32, dy: i32) void {
    self.X = self.X + dx
    self.Y = self.Y + dy
}

fn Point::Consume(*self) void {
}

fn Point::New() Self {
    return .Point{}
}
```

Rules:

- attached methods use `fn Type::Name(...)`
- if the first parameter is a receiver form, it is an instance method
- if there is no receiver parameter, it is a static method

Allowed receiver spellings:

- `self`
- `&self`
- `&mut self`
- `*self`

Current implementation notes:

- non-`self` receiver binder names are still accepted in some paths, but `self` is the intended style
- method calls on values still support auto-deref/auto-borrow behavior for method/field ergonomics

### Method Call Ergonomics

Current intended ergonomic behavior:

- method and field access may auto-deref through pointer/reference layers where appropriate
- plain expression contexts should **not** auto-deref references in general

Example:

```ferret
p.Incr()
p.Value
```

can resolve through receiver/field auto-deref, but:

```ferret
print(refValue)
```

does **not** auto-deref; direct printing of `&T` / `&mut T` is rejected.

---

## 11. `Self`

`Self` currently exists as a type placeholder.

It is valid in:

- attached method signatures
- interface method signatures

Examples:

```ferret
fn Point::New() Self

type Shape interface {
    New() Self
}
```

Current behavior:

- `Self` is instantiated against the attached/implementing concrete type

---

## 12. Interfaces

Example:

```ferret
type Shape interface {
    Draw(&self)
    MoveBy(&mut self, dx: i32, dy: i32)
    New() Self
}
```

Implementation rules:

- interface matching checks:
  - method name
  - receiver form
  - parameter list
  - result type
- receiver form is part of identity
- `Self` in the interface is instantiated to the concrete type during matching

So these are distinct:

- `Draw(self)`
- `Draw(&self)`
- `Draw(&mut self)`
- `Draw(*self)`

---

## 13. Composite Literals

Current literal forms:

- contextual composite literal:
  - `.{ ... }`
- typed composite literal:
  - `.Point{ ... }`

Examples:

```ferret
let p: Point = .{}
let q = .Point{ .X = 1, .Y = 2 }
```

Current behavior:

- `.{}`
  uses contextual type if available
- `.Type{}`
  is explicit and supported

---

## 14. Arrays

Current array types:

- `[N]T`
- `[_]T`

Examples:

```ferret
let a: [3]i32 = [3]i32{1, 2, 3}
let b: [_]i32 = [_]i32{1, 2, 3}
```

Current behavior:

- `[_]T` infers array length from the literal
- array indexing is implemented end-to-end
- array literals currently use typed brace form such as `[3]i32{1, 2, 3}` or `[_]i32{1, 2, 3}`

Note:

- future syntax may move to `[N]T{...}` / `[]T{...}`, but the current compiler still accepts the current array literal form it already implements

---

## 15. Slices

Current slice type:

- `[]T`

Current implementation status:

- slice type exists
- slice indexing exists in typechecking and backend paths
- slice literals are currently rejected as “not yet implemented”

This area is still under active design discussion.

The compiler currently has slice support in the type system and lowering paths, but the final surface semantics are not yet settled.

---

## 16. Strings

Current builtin string-like type:

- `str`

Current status:

- `str` exists and works in the compiler/runtime paths
- string literals are handled specially and flow through the current backend/runtime model
- the final semantic design of `str` is **not yet settled**

Discussion is still open on:

- whether string literals should always type as `str`
- how `str` should relate to slices/bytes/chars
- whether `str` is best modeled as an immutable view

So for now:

- `str` is implemented
- its final language semantics are still open

---

## 17. Borrow Rules

Current compiler behavior enforces:

- mutable borrow exclusivity
- immutable/mutable overlap rejection
- no reference escape into:
  - module-level bindings
  - heap-owned values
  - return values where forbidden
  - deferred capture

Current borrow lifetime behavior:

- ordinary borrows use NLL-style shortening after last use inside a block
- deferred uses are pinned to scope end

Example intended behavior:

```ferret
let y = &mut x
*y = 12
print(x)   // rejected if y is still live and used later
print(*y)
```

---

## 18. Printing And References

Current rule:

- direct printing of `&T` / `&mut T` is rejected
- dereference explicitly to print the pointee value

Example:

```ferret
print(y)   // error if y is &T or &mut T
print(*y)  // ok
```

This is intentional to avoid mixing:

- references
- pointee values
- raw addresses

---

## 19. Unsafe

Unsafe is currently required for raw-pointer-sensitive operations.

Examples:

```ferret
unsafe {
    let p = @x
}
```

`^T` is the raw-pointer category and should remain distinct from references.

---

## 20. Current Non-Goals / Incomplete Areas

These areas are intentionally incomplete or still under design discussion:

- `copy` deep-clone semantics
- final slice literal syntax and slice semantics
- final `str` semantics
- complete documentation pass across all docs
- broader stabilization around corner cases

---

## 21. Practical Summary

Current Ferret semantics are best summarized as:

- values are plain and copyable by default
- `*T` is the owner category
- `&T` / `&mut T` are borrow categories
- `^T` is the raw-pointer category
- method syntax is `Type::Method(...)`
- interfaces include receiver form in the signature
- visibility is currently case-based
- constructors/destructors are ordinary methods/functions now, not special language features
- slices and strings exist, but their final design is still being finalized
