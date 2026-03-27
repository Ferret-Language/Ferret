# Agent Workflow

Follow [RULES.md](RULES.md) for every code change in this repository.

## Required Pre-Change Check

Before editing code, answer these questions in your rationale:

1. What existing function/module already implements part of this behavior?
2. Can existing logic be reused directly instead of adding a wrapper?
3. Would this change duplicate logic across files, phases, or backends?
4. If a new helper is introduced, which rule in `RULES.md` allows it?

Do not start implementation until those questions are answered.

## Hard Constraints

- Do not add pass-through wrappers.
- Do not duplicate logic that can be centralized.
- Prefer existing shared logic before introducing new helpers.
- Keep diffs minimal and task-focused.
- Do not mix unrelated refactors into the same change.

## Stepwise Workflow

1. Keep a persistent plan file in the repo.
2. Implement one approved step at a time.
3. Stop after each step and wait for review.
4. Commit only after explicit approval.

## Required Close-Out Note

For each completed step, include a short `Rules check` note that states:

- whether any wrapper was added
- whether any duplicated logic remains in touched areas
- whether any helper was added and why it is allowed under `RULES.md`
