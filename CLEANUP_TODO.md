# Compiler Cleanup TODO

Current focus: remove duplicated formatting and signature-rendering code before more generic and tooling work lands on top of it.

## Formatting and Signature Cleanup

- [*] audit duplicate function/signature formatting paths across `ast`, `typeinfo`, `lsp`, `hir`, and `mir`
- [*] extract a shared formatter for `ast.FuncDecl` plus semantic `typeinfo.FuncType` so LSP does not keep its own signature builder
- [*] remove duplicate owner-qualified function-name rendering logic from LSP and AST formatting helpers
- [*] unify `typeinfo.FormatFuncSignature` and `(*typeinfo.FuncType).String()` so function-type rendering has one canonical implementation
- [*] decide whether param modifiers like `mut` and `comptime` belong in canonical function-type string output or only in explicit formatter helpers
- [*] reduce duplicated parameter-formatting logic between AST, HIR, and MIR printers
- [*] reduce duplicated receiver and method-header formatting between AST, LSP, HIR, and MIR printers
- [*] reduce duplicated interface-method signature formatting between AST and semantic type rendering

## Validation

- [ ] add focused regression tests for any extracted shared formatter so hover, AST signatures, and semantic signatures stay in sync
- [ ] re-run full compiler, LSP, and backend tests after each cleanup slice
