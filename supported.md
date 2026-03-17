# Ferret Backend Support Matrix

Status of language features through **both** the QBE and LLVM backends.
"Both backends" means the feature produces correct native code via `ferret -build-backend qbe` and `ferret -build-backend llvm` for the currently supported subset.

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
| `f64` | ⚠️ partial / not fully covered |
| `char` (mapped to `i32`) | ✅ |
| `str` | ✅ |

---

## Compound Types

| Type | Both backends |
|------|:---:|
| Named `struct` | ✅ |
| Anonymous `struct` | ✅ |
| Array `[T; N]` | ✅ |
| Owning pointer `*T` | ✅ |
| Reference `&T` / `&mut T` | ✅ |
| Raw pointer `^T` | ✅ |
| Tuple | ⚠️ frontend only |
| Named `enum` | ✅ |
| Named `union` (tagged runtime model) | ✅ |
| Named `interface` | ✅ |
| Named `error` | ⚠️ frontend only |
| Optional `?T` | ✅ |
| Error union `E!T` | ⚠️ frontend only |

---

## Declarations

| Feature | Both backends |
|---------|:---:|
| Top-level `fn` | ✅ |
| External methods with receivers | ✅ |
| `type Name struct { ... }` | ✅ |
| `type Name move ...` | ❌ removed |
| `type Name enum { ... }` | ✅ |
| `type Name union { ... }` | ✅ |
| `type Name interface { ... }` | ✅ |
| `type Name error { ... }` | ⚠️ frontend only |
| Local `let` / `let mut` | ✅ |
| Local `const` | ✅ |
| Top-level `const` | ✅ |
| Module-level `let` (globals) | ✅ |
| `#[extern("sym")] fn` | ✅ |
| `#[builtin] fn` | ✅ |
| `#[if(...)]` / `#[ifnot(...)]` declaration filtering | ✅ |
| Constructor syntax | ✅ |
| Destructor syntax | ✅ |
| Static fields | ⚠️ frontend / semantic only |

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
| `x as T` numeric casts | ✅ |
| `number as str` | ✅ |
| Union member selection/extraction casts | ✅ |
| `&x` address-of | ✅ |
| `*p` pointer dereference (read) | ✅ |
| `f(args)` function call | ✅ |
| `recv.method(args)` method call | ✅ |
| `Module::func(args)` cross-module call | ✅ |
| `.{ .Field = val }` composite literal | ✅ |
| `expr.field` field access (read) | ✅ |
| `arr[i]` array index (read) | ✅ |
| `copy expr` | ✅ |
| `comptime expr` | ⚠️ frontend / const-fold only |
| `catch` fallback | ⚠️ frontend only |
| `!!` force-unwrap | ⚠️ frontend only |
| `match expr { ... }` | ✅ |
| `is T` runtime type test on unions / optionals | ✅ |
| Function literals / lambdas | ❌ not yet |
| IIFE | ❌ not yet |

---

## Statements

| Statement | Both backends |
|-----------|:---:|
| `let x = expr` / `let x: T = expr` | ✅ |
| `x = expr` assignment | ✅ |
| `x.field = expr` field store | ✅ |
| `x[i] = expr` index store | ✅ |
| `unsafe { *p = expr }` pointer store | ✅ |
| Compound assignment | ✅ |
| `x++` / `x--` | ✅ |
| `return [expr]` | ✅ |
| `if expr { }` / `if expr { } else { }` | ✅ |
| `while expr { }` | ✅ |
| `for iterable \|v\| { }` / `for iterable \|i, v\| { }` | ✅ |
| `break` / `continue` | ✅ |
| Labeled `break` / `continue` | ✅ |
| `match expr { arm => expr }` | ✅ |
| `match expr { arm => { ... } }` | ✅ |
| `panic expr` | ✅ |
| `unsafe { }` block | ✅ |
| `defer ...` | ⚠️ MIR/CFG modeled, not codegen'd |
| `lock ... as name { ... }` | ⚠️ MIR/CFG modeled, not codegen'd |

---

## Match / Narrowing

| Feature | Both backends |
|---------|:---:|
| Literal match arms | ✅ |
| Wildcard `_` arm | ✅ |
| Typed arms `is T =>` | ✅ |
| Inline arm expressions `=> expr` | ✅ |
| Arm blocks `=> { ... }` | ✅ |
| Narrow matched value inside typed arm | ✅ |
| Typed arm binding `is T name =>` | ❌ removed |
| Exhaustiveness diagnostics | ⚠️ partial |
| Overlap diagnostics | ⚠️ partial |

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
| `std/*` stdlib imports | ✅ |
| Manifest (`fer.ret`) dependency loading | ✅ |
| Import cycle detection | ✅ |

---

## Entry Point

Both backends recognize the `main` module specially:

- the `main` function in a module named `main` is emitted directly as the platform entry symbol
- for any other module name the backend emits a thin trampoline to the Ferret `main`

---

## Not Yet Implemented

- Closures, capture blocks, anonymous functions
- `defer` codegen
- `lock` codegen
- `error` / `E!T` runtime representation and codegen
- Tuple codegen
- Full `f64` coverage
- Object files / static/dynamic libraries
- Optimization pipeline beyond current MIR cleanup/folding
