## Ferret Literal Typing Spec

## Print
Print uses Printable union as param. 

```ferret
// Printable union defines all types that can be printed
type Printable union {
    i8,
    i16,
    i32,
    i64,
    i128,
    i256,
    u8,
    u16,
    u32,
    u64,
    u128,
    u256,
    f32,
    f64,
    f128,
    f256,
    str,
    byte,
    bool
};
```

### 1. Untyped literals

* All numeric literals start untyped internally (in AST/HIR).
* Example:

```ferret
let a := 10   // 10 is untyped
```

### 2. Default collapse

* When a literal is used in a context without explicit type:

  * Collapse to default type `i32`.
  * If literal does not fit default → compile-time error; user must provide explicit type.

```ferret
let a := 10           // collapses to i32
let b := 5000000000   // ❌ error → must write: let b: i64 = 5000000000
```

### 3. Collapse triggers (contexts)

* Assignment to typed variable
* Passing to function parameter with type
* Used in arithmetic or expression with typed operands
* Passed to function expecting union of primitives → pick smallest compatible type

```ferret
let x: i64 = 5000000000   // explicit type → collapses to i64
let y = a + x as i64       // arithmetic → literal/variable collapses to match operand type
io::Print(1000)                // literal collapses to i32 (fits default)
io::Print(5000000000)          // literal collapses to i64 automatically
```

### 4. Arithmetic rules

* Operands must have same type
* Use `as` for explicit casting if sizes differ

```ferret
let a: i32 = 10
let b: i64 = 5000000000
let c = a as i64 + b  // ✅ works
```

### 5. Structs / Arrays

* Each literal field or element collapses individually
* Default i32 unless explicit type
* Error if literal exceeds default → explicit type required

```ferret
let s := { .X = 10, .Y = 50000 } // OK
let s2 := { .X = 10, .Y = 5000000000 }    // ❌ error, must cast. because 5000000000 > i32
```

### ✅ Summary Rule

1. Literals are always untyped
2. Collapse to DEFAULT_INT_TYPE/DEFAULT_FLOAT_TYPE by default when context not found
3. If literal cannot fit default → error, force explicit type in that case
4. Arithmetic requires matching types; use `as` if needed
5. Struct fields, array elements, function args follow same rules