# Ferret Language Specification (Draft)

## 1. Design Goals

* Predictable ownership and memory safety
* Minimal rules (no hidden behavior)
* No ambiguity between stack, heap, and references
* No duplication in interface implementation
* No lifetime system complexity (avoid Rust-level analysis)

---

## 2. Type System

### 2.1 Value Types (Stack)

Syntax:

```
T
```

Properties:

* Stored on stack
* Copyable
* Passed by value
* No implicit heap allocation

---

### 2.2 Owning Heap Pointer

Syntax:

```
*T
```

Properties:

* Non-null
* Owns heap allocation
* Move-only (non-copyable)
* Single owner at a time

---

### 2.3 References (Borrowed)

Syntax:

```
&T
&mut T
```

Properties:

* Non-owning
* Stack-only
* Temporary
* Cannot escape scope
* Cannot be stored in heap

---

### 2.4 Raw Pointer (FFI)

Syntax:

```
^T
```

Properties:

* Unsafe
* No guarantees
* Allowed only in unsafe context

---

## 3. Mutability

### 3.1 Binding Mutability

```
let x T
let mut x T
```

Rule:

* Mutation requires `mut` binding

---

### 3.2 Mutation Rules

To mutate:

* Binding must be `mut`
* Access must allow mutation (`&mut` or owning pointer)

---

## 4. Ownership Model

### 4.1 Move Semantics

```
let a *T
let b = a  // move
```

Rule:

* `*T` cannot be copied
* Assignment transfers ownership

---

### 4.2 Use After Move

Using a moved value is a compile-time error

---

### 4.3 Destruction

* Ownership must be consumed
* Automatically at end of scope
* Or via consuming function

---

## 5. Borrowing

### 5.1 Immutable Borrow

```
&T
```

* Multiple allowed
* No mutation allowed

---

### 5.2 Mutable Borrow

```
&mut T
```

* Only one active mutable borrow
* No other borrows allowed simultaneously

---

### 5.3 Borrow Scope

* Borrow cannot outlive source
* Cannot return reference to local value

---

### 5.4 Heap Restriction

Forbidden:

```
*&T
*&mut T
```

Rule:

* Heap memory cannot contain references

---

## 6. Parameter Passing (Core Rule)

> All semantics are defined by parameter passing

| Type   | Behavior         |
| ------ | ---------------- |
| T      | Copy             |
| *T     | Move             |
| &T     | Immutable borrow |
| &mut T | Mutable borrow   |

---

## 7. Methods (Receiver Model)

### 7.1 Definition

Methods are functions with a receiver parameter:

```
fn (x T) method(...)
fn (x *T) method(...)
fn (x &T) method(...)
fn (x &mut T) method(...)
```

### 7.2 Semantics

Receiver follows parameter rules:

* `T` → copy
* `*T` → move
* `&T` → read
* `&mut T` → mutate

No special method behavior exists

---

## 8. Interfaces

### 8.1 Definition

```
type Reader interface {
    &mut read(buf []u8) i32
}
```

### 8.2 Rules

* Defines required methods
* Includes receiver modifier
* No receiver type specified

---

### 8.3 Satisfaction

* No `implements` keyword
* A type satisfies an interface when used
* Compiler verifies method existence

---

### 8.4 Matching

A method matches if:

* Name matches
* Receiver modifier matches
* Parameters match
* Return type matches

---

### 8.5 No Duplication

* Methods defined once per type
* Multiple interfaces reuse same method

---

## 9. Heap Access

### 9.1 Access

```
p.value
```

### 9.2 Mutation

Requires `mut` binding

### 9.3 Pointer Reassignment

```
p = p.next
```

---

## 10. Safety Guarantees

Prevents:

* Double free
* Use-after-free
* Null dereference
* Mutable aliasing
* Dangling references in heap

---

## 11. Restrictions

1. `*T` is non-copyable
2. References cannot be heap-stored
3. No mutable aliasing
4. No use after move
5. No escaping references

---

## 12. Mental Model

* Stack values → copyable
* Heap values → single owner
* References → temporary views
* Raw pointers → unsafe

---

## 13. Summary Rule

> Parameter passing defines all semantics. No exceptions.