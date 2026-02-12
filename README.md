# Ferret Compiler

A modern compiler for the Ferret programming language.
Backend: Ferret uses the official QBE backend as a submodule (git://c9x.me/qbe.git).

## Design philosophy
1. **It does what it looks like.**: The language syntax and semantics are designed to be intuitive and symbol_table.
2. **Helpful error messages.**: The compiler provides clear and actionable error messages to help developers fix issues quickly.
3. **Nearly 0 runtime error**: The type system and compile-time checks aim to eliminate runtime errors as much as possible.
4. **Fast compilation.**: The compiler is optimized for speed to provide a smooth development experience
## Quick Start

### Initialize submodules

```bash
git submodule update --init --recursive
```

### Update or switch QBE submodule

Fetch the latest commit on the tracked branch (set in `.gitmodules`):

```bash
git submodule update --remote --merge qbe
```

Switch the submodule to another branch and update:

```bash
git submodule set-branch --branch <branch> qbe
git submodule update --remote --merge qbe
```

Pin the submodule to a specific commit:

```bash
cd qbe
git checkout <commit-sha>
cd ..
git add qbe
```

After any submodule change, commit the updated `qbe` gitlink and `.gitmodules`.

### Install C toolchain

Linux/macOS:
```bash
./scripts/install-toolchain.sh
```

Windows (PowerShell):
```powershell
.\scripts\install-toolchain.ps1
```

### Run the compiler

```batch
./scripts/run.bat test/simple.fer
```

That's it! The script uses `go run` to compile and run directly.

### Build
To build the compiler binary, run:

```batch
./scripts/build.bat
```
Or to build for both wasm and native:

```batch
./scripts/build-all.bat
```

### Testing

```batch
go build -o ./bin/ferret main.go && ./bin/ferret code.fer
```
Note: flags must be before any non flag arguments in ferret command. like `./bin/ferret -o execname code.fer`, but `./bin/ferret code.fer -o execname` will not work.

## Language Features

### Variables and Constants

```ferret
let x := 10;              // Type inference
let y: i32 = 20;          // Explicit type
const pi := 3.14;         // Constant
```
There is nothing like truethy or falsy values in Ferret. Only `bool` type is used for boolean logic.

### Types

```ferret
// Basic types
i8, i16, i32, i64         // Signed integers
u8, u16, u32, u64         // Unsigned integers
f32, f64                  // Floats
str, bool, byte           // String, boolean, byte

// Arrays
[3]i32                    // Fixed-size array (compile-time bounds checking)
[]i32                     // Dynamic array (auto-grows, no bounds checking)
// Both support negative indexing: arr[-1] accesses last element

// Optional types
?i32                      // Nullable integer

// Result types
Error ! Data              // Used with functions that can fail

fn get_data() -> str ! i32 { // Error type first, success type second
    // ...
    if fail {
        return "Failed to get data"!;  // returning str as error. The `!` operator marks an expression as error
    }

    return 42; // returning i32 as success value
}
```

### Structs

```ferret
type Point struct {
    .x: f64,
    .y: f64
};

let p : Point = { .x = 1.0, .y = 2.0 }; // From the value (anonymous struct literal), type is inferred to Point because of the variable type
````
But what if the type is not specified and we want to create a Point from the literal?
Other languages may use this syntax: Point{ x: 1.0, y: 2.0 }. But we keep behaviors consistent and use the same syntax for both cases.
```ferret
let p2 := { .x = 3.0, .y = 4.0 } as Point; // Here we use `as` to cast the anonymous struct literal to Point.
```
Every type can be anonymous. Like structs, interface, enums, functions etc.

### Enums

```ferret
type Color enum {
    Red,
    Green,
    Blue
};

let color := Color::Red;
```

The `::` operator is used to access static members of types like enums and modules (symbols on other files).

### Exports and Visibility (Go-style Capitalization)

Ferret uses **capitalization** to control visibility (like Go):
- **Uppercase** names are **exported** (public)
- **Lowercase** names are **private**

```ferret
// Module-level symbols
const MAX := 100;             // Exported (uppercase)
const internal := 42;          // Private (lowercase)

fn Add(a: i32, b: i32) -> i32 { ... }  // Exported function
fn helper() { ... }                      // Private function

type Point struct {            // Exported type
    .X: i32,                   // Exported field (uppercase)
    .y: i32                    // Private field (lowercase)
};

type internal struct { ... };  // Private type
```

**Struct field visibility:**
- Uppercase fields (`.X`, `.Name`) are **public** - accessible everywhere
- Lowercase fields (`.x`, `.name`) are **private** - only accessible:
  - Within methods of the same type (via receiver)
  - In struct literal construction (to provide values)
- Direct field access `obj.field` on private fields is **not allowed** outside methods

```ferret
// In any module
type Point struct {
    .X: i32,  // public - accessible everywhere
    .y: i32   // private - restricted access
};

fn (p: Point) GetY() -> i32 {
    return p.y;  // ✅ OK - method can access private field
}

fn (p: &Point) SetY(val: i32) {
    p.y = val;   // ✅ OK - method can modify private field
}

fn test() {
    // ✅ OK - struct literal can set all fields
    let p := { .X = 10, .y = 20 } as Point;

    let x := p.X;     // ✅ OK - public field
    let y := p.y;     // ❌ Error - private field access
    let y2 := p.GetY();  // ✅ OK - use method for controlled access
}
```

**Cross-module access:**
```ferret
// In module A
type Point struct {
    .X: i32,  // public
    .y: i32   // private
};

// In module B
import "moduleA";

let p := { .X = 10, .y = 20 } as moduleA::Point;  // ✅ Can construct
let x := p.X;     // ✅ OK - public field
let y := p.y;     // ❌ Error - private field
```

**Enum variants inherit visibility:**
```ferret
type Color enum { Red, Green, Blue };  // Exported enum
// Color::Red, Color::Green, Color::Blue are all exported

type internal enum { A, B };  // Private enum
// internal::A and internal::B are also private
```

### Module System & Import Restrictions

```ferret
import "std/math";
// The alias 'math' is now reserved
// Cannot declare variables, constants, functions, or types named 'math'

// This would cause a compile error:
// let math := 42;  // Error: 'math' is already used as an import alias

// Use custom aliases to avoid conflicts:
import "std/math" as m;
let math := 42;  // OK now, 'm' is the import alias
```

### Functions

```ferret
fn add(a: i32, b: i32) -> i32 {
    return a + b;
}
```

### Optional Types & Coalescing Operator

```ferret
let maybe: ?i32 = None;
let value := maybe ?? 42;  // Defaults to 42 if None. Kind of like `??` in other languages. Or we may switch to `??` later. making ?? a ternary operator.
```

### Error Handling

```ferret
fn divide(a: i32, b: i32) -> i32 ! Error {
    if b == 0 {
        return Error{ .msg = "Division by zero" }!;
    }
    return a / b;
}

const result := divide(10, 2) catch err {
    println("Error: {}", err.msg);
} 0;  // Fallback value
```

If you return from the handler block, the function will return early with that value and you don't need to provide a fallback value.
Also you can use shorthand syntax like,
```ferret
const result := divide(10, 2) catch 0;  // Fallback value
```

### Arrays and Indexing

```ferret
// Fixed-size arrays with compile-time bounds checking
let arr: [5]i32 = [1, 2, 3, 4, 5];
let x := arr[2];   // OK
let y := arr[10];  // Compile error: index out of bounds

// Dynamic arrays auto-grow (no bounds checking)
let dyn := [1, 2, 4];  // size 3
dyn[5] = 43;           // grows to [1, 2, 4, 0, 0, 43]

// Negative indexing (both fixed-size and dynamic)
let last := arr[-1];   // Last element
let second_last := arr[-2];  // Second to last

// Dynamic arrays (runtime bounds checking)
let dyn: []i32 = [1, 2, 3];
let val := dyn[100];  // Runtime check, not compile-time
```

### Control Flow

```ferret
if x < y {
    println("x is less");
} else {
    println("x is greater or equal");
}

for i, v in arrayLike { // i is read-only (loop index)
    println(i, v);
}

// skip index or value using _
for _, v in arrayLike {
    println(v);
}
for i, _ in arrayLike {
    println(i);
}
for _, _ in arrayLike {
    // do something
}

while x < 5 {
    print(x);
    x = x + 1;
}

// match statement
match value {
    1 => println("one"),
    2 => println("two"),
    _ => println("other") // default case
}
```


## Error Reporting

Ferret provides beautiful, helpful error messages:

```
error[T0001]: type mismatch
  --> example.fer:10:15
   |
10 | let num: i32 = "hello";
   |                ~~~~~~~ expected i32, found str
   |
  = note: type inference determined this expression has type 'str'
  = help: convert the string to an integer using the parse function

Compilation failed with 1 error(s)



error: integer literal 200 overflows i8
  --> example.fer:13:13
   |
12 | let e: i8 = 100;  // OK: literal 100 fits in i8
13 | let f: i8 = 200;  // Error: 200 doesn't fit in i8 (-128 to 127)
   |     -       ~~~ need at least u8 or i16
   |     |
   |     -- type 'i8'
   |
   = help: i8 can hold values in range: -128 to 127

```

## License

See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please read the documentation to understand the compiler architecture before submitting PRs.

## Note to new contributors
Feel free to open issues or ask questions in the discussions section. We appreciate your interest in improving Ferret!
