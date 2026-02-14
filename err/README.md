# Error Demos

These files are intentionally invalid Ferret programs.
Use them to inspect compiler diagnostics and fix-it hints.

## Run

```bash
./bin/ferret -o /tmp/app err/move_wraps_borrow.fer
./bin/ferret -o /tmp/app err/borrow_wraps_move.fer
./bin/ferret -o /tmp/app err/mut_borrow_wraps_move.fer
./bin/ferret -o /tmp/app err/move_param_missing_at.fer
```

Expected:
- `move_wraps_borrow.fer`: `cannot combine move with borrow` with `-`/`+` replacement hint.
- `borrow_wraps_move.fer`: `cannot combine borrow with move` with `-`/`+` replacement hint.
- `mut_borrow_wraps_move.fer`: same as above for mutable borrow form.
- `move_param_missing_at.fer`: move-qualified parameter requires explicit `@`.
