# Ownership Examples

Run from `compiler/`:

```bash
go run ./cmd/ferret ./examples/ownership/valid/00_other_field_after_partial_move.ferr
go run ./cmd/ferret ./examples/ownership/valid/01_field_reinit_after_partial_move.ferr
go run ./cmd/ferret ./examples/ownership/invalid/00_partial_move_whole_use.ferr
```
