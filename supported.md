# Ferret Backend Support Matrix

Status of language features through **both** the QBE and LLVM backends.  
"Both backends" means the feature produces correct native code via `ferret -build-backend qbe` and `ferret -build-backend llvm`.

---

## Scalar Types

| Type | Both backends |
|------|:---:|
| `bool` | ✅ |
| `i8` / `u8` | ✅ |
| `i16` / `u16` | ✅ |
| `i32` / `u32` | ✅ |
| `i64` / `u64` | ✅ |
| `isize` / `usize` | ✅ |
| `f32` | ✅ |
| `f64` | ⚠️ frontend only |
| `char` (mapped to i32) | ✅ |
| `str` / string type | ⚠️ string globals only |

---

## Compound Types

| Type | Both backends |
|------|:---:|
| Named `struct` | ✅ |
| Anonymous struct | ✅ |
| Array `[T; N]` | ✅ |
| Pointer `*T` / `*mut T` | ✅ |
| Tuple | ⚠️ frontend only |
| Named `enum` | ✅ |
| Named `union` (safe) | ⚠️ frontend only |
| Named `interface` | ⚠️ frontend only |
| Named `error` | ⚠️ frontend only |
| Optional `?T` | ⚠️ frontend only |
| Error union `E!T` | ⚠️ frontend only |

---

## Declarations

| Feature | Both backends |
|---------|:---:|
| Top-level `fn` | ✅ |
| `fn (recv T) method()` external methods | ✅ |
| `type Name struct { ... }` | ✅ |
| `type Name move ...` | ✅ |
| `type Name enum { ... }` | ⚠️ frontend only |
| `type Name union { ... }` | ⚠️ frontend only |
| `type Name interface { ... }` | ⚠️ frontend only |
| `type Name error { ... }` | ⚠️ frontend only |
| Local `let` / `let mut` | ✅ |
| Local `const` | ✅ |
| Top-level `const` | ✅ |
| Module-level `let` (globals) | ✅ |
| `#[extern("sym")] fn` | ✅ |
| `#[builtin] fn` | ✅ |
| `#[if(debug)]` / `#[if(target_os, "...")]` top-level filtering | ✅ |
| Constructor `fn new(...)` syntax | ⚠️ frontend only |
| Destructor syntax | ⚠️ frontend only |
| Static fields | ⚠️ frontend only |

---

## Expressions

| Expression | Both backends |
|------------|:---:|
| Integer / bool / string literals | ✅ |
| `none` | ✅ |
| Binary arithmetic `+` `-` `*` `/` `%` | ✅ |
| Bitwise `&` `\|` `^` `<<` `>>` | ✅ |
| Comparison `==` `!=` `<` `>` `<=` `>=` | ✅ |
| Logical `&&` `\|\|` | ✅ |
| Unary `-` `!` | ✅ |
| `x as T` (numeric casts) | ✅ |
| `&x` address-of | ✅ |
| `*p` pointer dereference (read) | ✅ |
| `unsafe *p` (unsafe context dereference) | ✅ |
| `f(args)` function call | ✅ |
| `recv.method(args)` method call | ✅ |
| `Module::func(args)` cross-module call | ✅ |
| `.{ .Field = val }` composite literal | ✅ |
| `expr.field` field access (read) | ✅ |
| `arr[i]` array index (read) | ✅ |
| `copy expr` | ✅ |
| `unsafe expr` | ❌ removed |
| `comptime expr` | ⚠️ frontend / const-fold only |
| `catch` fallback | ⚠️ frontend only |
| `!!` force-unwrap | ⚠️ frontend only |
| Function literals / lambdas | ❌ not yet |
| IIFE `(fn() { ... })()` | ❌ not yet |

---

## Statements

| Statement | Both backends |
|-----------|:---:|
| `let x = expr` / `let x: T = expr` | ✅ |
| `x = expr` assignment | ✅ |
| `x.field = expr` field store | ✅ |
| `x[i] = expr` index store | ✅ |
| `unsafe { *p = expr }` pointer store | ✅ |
| `x += expr` `-=` `*=` `/=` `%=` compound assign | ✅ |
| `x &= expr` `\|=` `^=` `<<=` `>>=` compound assign | ✅ |
| `x++` / `x--` increment / decrement | ✅ |
| `return [expr]` | ✅ |
| `if expr { }` / `if expr { } else { }` | ✅ |
| `while expr { }` | ✅ |
| `for val \|v\| { }` / `for val \|i, v\| { }` | ✅ |
| `break` / `continue` | ✅ |
| `break 'label` / `continue 'label` | ✅ |
| `match expr { pattern => { } ... }` | ✅ |
| `panic expr` | ✅ |
| `unsafe { }` block | ✅ |
| `defer { }` | ⚠️ MIR modeled, not codegen'd |
| `lock { }` | ⚠️ MIR modeled, not codegen'd |

---

## Module System

| Feature | Status |
|---------|--------|
| Single-file compile | ✅ |
| Workspace / multi-module compile | ✅ |
| `import "path"` | ✅ |
| `import "path" as alias` | ✅ |
| Cross-module type references | ✅ |
| Cross-module function calls | ✅ |
| `std/*` stdlib imports | ✅ (declarations resolved) |
| Manifest (`fer.ret`) dependency loading | ✅ |
| Import cycle detection | ✅ |

---

## Entry Point

Both backends recognize the `main` module specially:

- The `main` function in a module named `main` is emitted directly as the
  platform entry symbol (`$main` / `@main`) without a generated wrapper.
- For any other module name the backend emits a thin `export $main` /
  `define @main` trampoline that calls the Ferret `main`.

---

## Not Yet Implemented

- Closures, capture blocks, anonymous functions
- `defer` codegen (MIR node exists; backends emit no instructions for it)
- `lock` codegen
- Enum / union / interface / error-union lowering
- Optional (`?T`) and error-union (`E!T`) runtime representation
- `!!` and `catch` codegen
- Tuple codegen
- Full `f64` coverage (may work incidentally; not tested)
- Object files / static/dynamic libraries (only executables today)
- Optimization pipeline (both backends use default unoptimized codegen)
