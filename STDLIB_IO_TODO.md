# Stdlib IO Todo (Go-Style Dispatch)

Goal: provide a common `Reader`/`Writer` interface surface so console, file, and network APIs can be used through one call pattern (similar to Go’s `io.Writer` usage style).

## 1) API Surface (Language-Level)

- [ ] define `std/io` core interfaces:
  - `Reader` (`Read(&mut self, buf: []u8) i32`)
  - `Writer` (`Write(&mut self, buf: []u8) i32`)
  - `Closer` (`Close(&mut self) void`)
  - `ReadWriter` composition pattern
- [ ] define common helper funcs in `std/io`:
  - `WriteAll(w: Writer, buf: []u8) i32`
  - `ReadAtLeast(r: Reader, buf: []u8, min: usize) i32`
  - `Copy(dst: Writer, src: Reader, scratch: []u8) i32`
- [ ] confirm receiver form consistency (`&self` vs `&mut self`) for interface implementers.

## 2) Std Modules

- [ ] add `std/os` adapters:
  - `Stdout()`, `Stderr()`, `Stdin()` concrete types implementing `Writer`/`Reader`
- [ ] add `std/fs` file handle type implementing `Reader`/`Writer`/`Closer`
- [ ] add `std/net` socket/conn baseline implementing `Reader`/`Writer`/`Closer` (after `std/io` + `std/fs` stability)

## 3) Runtime C Layout Cleanup

Current runtime is centralized in `runtime/ferret_runtime.c/h`.

- [ ] split runtime by concern while keeping one public include boundary:
  - `runtime/ferret_runtime.h` (public surface)
  - `runtime/ferret_alloc.c`
  - `runtime/ferret_io_stdio.c`
  - `runtime/ferret_io_file.c`
  - `runtime/ferret_net.c` (optional/phase-2)
- [ ] keep naming/mangling stable for Ferret stdlib extern linkage.
- [ ] ensure build scripts include new runtime units for the LLVM flow.

## 4) Compiler/Backend Checks (Only If Blocked)

- [ ] verify interface dispatch for `&mut self` methods with slice params.
- [ ] verify extern linking for new stdlib/runtime symbols across module paths.
- [ ] add minimal diagnostics when an interface method receiver shape mismatches implementation.

## 5) Validation Matrix

- [ ] add smoke: `io_writer_smoke.fer` (single API call works with stdout writer)
- [ ] add smoke: `io_file_smoke.fer` (write/read/close roundtrip via `std/fs`)
- [ ] add smoke: `io_copy_smoke.fer` (copy stdin/file/network-like reader->writer)
- [ ] run backends:
  - `ferret -backend llvm ...`

## 6) Done Criteria

- [ ] one `WriteAll` path works unchanged for stdout and file.
- [ ] one `Copy` path works for two different concrete implementations.
- [ ] no backend-specific behavior divergence in smoke suite.
- [ ] docs updated in `supported.md` + language/runtime notes.
