# Ferret Semantics: Ownership, Stack/Heap, Move, and Borrow

This document defines the intended semantics for Ferret ownership and references.

## 1. Value Kinds

Ferret has two ownership-bearing value kinds:

- `T`: stack-owned value
- `#T`: heap-owned boxed value

`#T` is an owning box type (similar to Rust `Box<T>`). It is a first-class type, not metadata.

## 2. Allocation

- `#expr` allocates a new heap box and produces `#T` where `T` is the value type of `expr`.
- Heap allocation is only valid for values that are not already heap-backed runtime containers.
  - Invalid: `#[]T`, `#map[K]V`, `#str`

## 3. Copy vs Move

Ferret is copy-by-default for all value categories. Move is explicit.

- `x` in assignment/call/return performs copy semantics.
- `@x` moves ownership out of `x`.
- After move, `x` is unusable until reinitialized.

Rules:

- Move is allowed only from movable bindings (locals/params/receiver), not constants/module globals.
- Move from references is invalid.
- `return @x` transfers ownership.

## 4. Borrow

Borrow operators:

- `&x` shared borrow (`&T`)
- `&mut x` mutable borrow (`&mut T`)

Rules:

- Multiple shared borrows are allowed.
- Exactly one mutable borrow is allowed at a time.
- Shared and mutable borrows cannot overlap for the same place.
- `&mut` cannot be taken through an immutable borrow chain.
- Borrows follow non-lexical lifetimes (NLL): borrow can end at last use before scope end.

## 5. Assignment and Ownership

### Stack values (`T`)

- Plain assignment/call/return copy the value.
- Explicit move uses `@`.

### Heap values (`#T`)

- `x: #T = #v` creates/rebinds heap ownership.
- `x = @y` (both `#T`) transfers heap ownership from `y` to `x`.
- `x = y` (both `#T`, no `@`) clones the heap payload into a new owner.
- `return y` where `y: #T` clones; `return @y` moves.

## 6. References to Heap Values

- Borrowing a `#T` value borrows its payload (`T`), not ownership.
- `@` is the ownership transfer operation for `#T`.

## 7. Function Boundaries

- `T` return: returns stack/value semantics.
- `#T` return: returns heap ownership.
- `return v` is copy semantics.
- `return @v` is move semantics.

## 8. Safety Invariants

Ferret must enforce:

- No use-after-move
- No aliasing violation (`&mut` vs `&`)
- No escaping references to dead stack locals
- Ownership state driven by type (`#T`), not by ad-hoc symbol flags
