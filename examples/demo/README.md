# Ferret Demo

This example is kept inside the subset that currently builds and runs on **both** backends.

## File

- `demo.ferr`

## What it demonstrates

- stdlib imports: `std/io`, `std/os`, `std/math`
- top-level conditional declarations with `#[if(...)]`
- named `enum`
- `move`-marked named type
- `while`
- `i++`
- numeric casts with `as`
- runtime OS/platform queries
- basic control flow and return-based self-checks

## Build and run

```bash
cd compiler
./bin/ferret -build-backend qbe -o /tmp/ferret_demo_qbe ./examples/demo/demo.ferr
./bin/ferret -build-backend llvm -o /tmp/ferret_demo_llvm ./examples/demo/demo.ferr

/tmp/ferret_demo_qbe
/tmp/ferret_demo_llvm
```

Expected output on this machine:

```text
Ferret demo
linux
amd64
Linux
```

Both binaries should exit with status `0`.
