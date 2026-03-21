# Session Resume: Allocator Status (2026-03-22)

## Final Validated Status

Allocator smoke tests are passing with the active PATH compiler (`ferret`):

```bash
ferret run allocator_smoke.ferr
# ok: system allocator
# ok: page allocator
# ok: arena allocator
# ok: allocator interface forwarding
# allocator smoke test: PASS

ferret run allocator_ffi_smoke.ferr
# allocator ffi smoke: PASS
```

## Important Environment Note

There are two different compiler binaries in this workspace:

- `ferret` -> `/home/fuad/Dev/Ferret-compiler-v2/compiler/build/core/bin/ferret` (v0.0.2)
- `./ferret` -> `/home/fuad/Dev/Ferret-compiler-v2/compiler/ferret` (v0.1.0)

`./ferret` currently fails allocator smoke files with parser/type cascades (around `^void` parameter parsing), while `ferret` passes.

## Recommended Command Convention

For consistent session behavior, use one binary everywhere (recommended: `ferret` from PATH) and avoid mixing with `./ferret` unless explicitly testing that binary.

## Next Session Follow-up

1. Decide which binary is canonical for development and tests.
2. If canonical is `./ferret`, port/fix allocator support there until both smoke tests pass.
3. Add a small version/path check in tooling/docs to prevent accidental mixed-binary runs.
