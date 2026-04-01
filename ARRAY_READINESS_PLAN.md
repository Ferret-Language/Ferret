## Array Readiness Plan

Goal: lock the current array surface down as a stable implemented feature.

Pre-change check:
- Existing array behavior already lives in the typechecker and backend lowering paths.
- Existing regression patterns already live in the backend lower tests and `tests/semma`.
- No wrapper/helper is needed; this task is readiness coverage and status documentation only.

Steps:
- [completed] Add explicit backend/readiness coverage for mutable array element writes and array-to-slice usage.
- [completed] Update status docs to reflect that the current array surface is implemented and covered.
- [completed] Run targeted backend tests and an end-to-end Ferret smoke file, then stop for review.
