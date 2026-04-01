## Tuple Readiness Plan

Goal: lock the current tuple surface down as a stable implemented feature.

Pre-change check:
- Existing tuple behavior already lives in the typechecker, aggregate layout, and backend lowering paths.
- Existing regression patterns already live in the typechecker/backend test files and `tests/semma`.
- No wrapper/helper is needed; this task is readiness coverage and status documentation only.

Steps:
- [completed] Add explicit typechecker and backend coverage for tuple indexing and tuple literals on the current supported surface.
- [completed] Update status docs to reflect that the current tuple surface is implemented, with compile-time indexing and positional literals.
- [completed] Run targeted validation and an end-to-end Ferret smoke file, then stop for review.
