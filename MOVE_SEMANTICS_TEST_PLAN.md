## Move Semantics Test Plan

Goal: pin down current Ferret value semantics for assignment, plain `T` parameters, `self` receivers, and interface value methods.

Pre-change check:
- Existing ownership behavior already lives in `internal/analysis/semantics/ownership/ownership.go`, especially `consumeMoveValue` and `receiverConsumes`.
- Existing regression style already lives in `internal/analysis/semantics/ownership/ownership_test.go`.
- No wrapper/helper is needed; this task is coverage only.

Steps:
- [completed] Add ownership regressions for copy types vs move types across assignment, function calls, methods, and interface value methods.
- [completed] Run focused ownership tests and capture the observed current behavior: plain assignment, plain `T` params, plain `self`, and interface value methods are still reusable even for move types.
