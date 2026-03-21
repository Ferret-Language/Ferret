# CTFE TODO

- [*] add initial CTFE evaluator entrypoint (implemented in MIR pass, not typechecker)
- [*] wire `requireConstExpr` to CTFE via MIR compile-time folding diagnostics
- [*] fold `comptime expr` into constant MIR values when CTFE succeeds
- [*] add CTFE evaluator pass in MIR and run it from the semantic pipeline after MIR validation
- [*] support CTFE literals: number, string, bool, none
- [*] support CTFE operators: unary (`-`, `!`) and common binary arithmetic/comparison/logical ops
- [*] support CTFE function calls for non-extern functions with bodies
- [*] support CTFE control flow in function bodies: `let`, `const`, assignment, `if`, `while`, `return`
- [*] add CTFE safety limits (max call depth / max step count)
- [*] add tests for CTFE success on function call + loop
- [*] add tests for CTFE failure on runtime/extern dependency
- [*] support CTFE for method/selector calls (`Type::Fn`, `recv.method`) across modules
- [*] support CTFE `struct` aggregates and field selector evaluation
- [*] support CTFE array aggregates and index evaluation
- [*] support CTFE tuple aggregates and index evaluation
- [*] support CTFE `for` loops
- [*] add end-to-end tests for library `assert(comptime cond, comptime msg)` pattern (including `comptime { assert(...) }`)
- [*] run full parser/typechecker/MIR/backend regression suites and fix CTFE side effects
