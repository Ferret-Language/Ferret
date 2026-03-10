# Parser Examples

This directory contains parser-focused Ferret examples.

Structure:

- `valid/`: source files that should parse successfully with the current frontend
- `invalid/`: source files that are intentionally malformed to exercise recovery and diagnostics
- `workspace/`: a small multi-file workspace for import and module parsing

Suggested commands:

```bash
cd /home/fuad/Dev/test/compiler
go run ./cmd/langc ./examples/parser/valid/00_decls_and_types.ferr
go run ./cmd/langc ./examples/parser/valid/01_control_flow.ferr
go run ./cmd/langc ./examples/parser/valid/02_special_forms.ferr
go run ./cmd/langc ./examples/parser/workspace
go run ./cmd/langc ./examples/parser/invalid/00_missing_expression.ferr
go run ./cmd/langc ./examples/parser/invalid/01_struct_recovery.ferr
go run ./cmd/langc ./examples/parser/invalid/02_mixed_composite_literal.ferr
```
