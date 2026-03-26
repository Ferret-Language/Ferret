# Work Plan

This file is the pause/resume tracker for active compiler work.

## Workflow

1. Keep changes scoped to one approved step at a time.
2. After each implementation step, stop for review.
3. Commit only after explicit approval.
4. Update this file whenever the active step or status changes.

## Current Focus

Topic: Comptime redesign and related compiler behavior

Status: Step 2 implemented and verified; waiting for review.

## Steps

- [done] Finalize the comptime language rules and implementation order.
- [done] Implement step 1: route `const` through CTFE and remove the syntax-only const gate.
- [done] Add repo workflow files (`AGENTS.md`, `WORK_PLAN.md`) for pause/resume and approval-first work.
- [done] Add `test_comptime/` repro files and verify them with a built compiler.
- [done] Review step 1 diff and repro results.
- [done] Commit step 1 only after approval.
- [done] Implement step 2: remove legacy `comptime` parameter syntax and plumbing.
- [done] Verify step 2 with focused tests and real Ferret repro files.
- [in_review] Wait for review before committing step 2.

## Active Task List

- [done] Verify that `const` initializers use CTFE on real Ferret source files.
- [done] Keep `WORK_PLAN.md` updated with concrete implementation tasks, not only workflow states.
- [done] After approval, commit only step 1 and its verification artifacts.
- [done] Remove `comptime` parameter support from parser, AST, semantic flags, HIR, MIR, and LSP.
- [done] Update tests and Ferret sample files to use plain parameters plus explicit `comptime` calls/blocks.
- [done] Rebuild compiler and run `test_comptime/` repros after the syntax removal.

## Verification

- `./build.sh`
- `./build/core/bin/ferret run:llvm test_comptime/const_local_ctfe.fer`
- `./build/core/bin/ferret run:qbe test_comptime/const_local_ctfe.fer`
- `./build/core/bin/ferret run:llvm test_comptime/const_call_ctfe.fer`
- `./build/core/bin/ferret run:qbe test_comptime/const_call_ctfe.fer`
- `./build/core/bin/ferret check test_comptime/const_runtime_call_should_fail.fer`

Observed results:

- `const_local_ctfe.fer` passed on both LLVM and QBE.
- `const_call_ctfe.fer` passed on both LLVM and QBE.
- `const_runtime_call_should_fail.fer` failed as expected through the CTFE path.

## Notes

- Follow `compiler/RULES.md` for every compiler change.
- Reuse existing logic before adding helpers.
- Add focused regression tests for each approved behavior change.
