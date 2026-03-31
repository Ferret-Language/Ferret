## Runtime Split Plan

Goal: split `runtime/ferret_runtime.c` into domain-specific source files without changing exported symbols, ABI, or behavior.

### Pre-change check

1. Existing behavior already lives in `runtime/ferret_runtime.c`, and the build packs every `runtime/*.c` file into the static runtime archive.
2. Reuse the existing runtime exports directly; do not add forwarding wrappers.
3. Avoid duplicating local helper logic by introducing one private runtime internal header only for helpers already shared across runtime domains.
4. A new helper/header is allowed because it centralizes shared runtime-local logic used in multiple compilation units.

### Steps

- [x] Add a private runtime internal header for shared local helpers.
- [x] Split panic/recover, print, string conversions, os, and mem helpers into separate `runtime/*.c` files.
- [x] Rebuild the runtime bundle and rerun a runtime smoke file to confirm behavior is unchanged.
