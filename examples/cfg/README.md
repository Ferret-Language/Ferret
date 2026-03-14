# CFG examples

Run from `compiler/`.

Valid examples:

```bash
go run ./cmd/ferret ./examples/cfg/valid/00_if_else_returns.ferr
go run ./cmd/ferret ./examples/cfg/valid/01_loops_and_labels.ferr
go run ./cmd/ferret ./examples/cfg/valid/02_nested_if_returns.ferr
go run ./cmd/ferret ./examples/cfg/valid/03_switch_fallback_return.ferr
```

Invalid examples:

```bash
go run ./cmd/ferret ./examples/cfg/invalid/00_missing_return_if.ferr
go run ./cmd/ferret ./examples/cfg/invalid/01_unreachable_after_return.ferr
go run ./cmd/ferret ./examples/cfg/invalid/02_missing_return_nested_if.ferr
go run ./cmd/ferret ./examples/cfg/invalid/03_missing_return_switch_fallback.ferr
```
