## Str Readiness Plan

Goal: lock down the current `str` surface as an immutable text view.

Pre-change check:
- Existing `str` behavior already lives in the typechecker explicit-cast rules, backend lowering, and runtime string helpers.
- Existing regression patterns already live in the typechecker/backend test files and `tests/semma`.
- No wrapper/helper is needed; this task is a focused semantic guard plus readiness coverage/documentation.

Steps:
- [completed] Update docs to state that `str` is the current immutable view-like text type.
- [completed] Reject casts from `str` to mutable byte/char slices in semantic checking.
- [completed] Add focused tests and a Ferret smoke file for supported readonly conversions and unsupported mutation-oriented casts, then stop for review.
