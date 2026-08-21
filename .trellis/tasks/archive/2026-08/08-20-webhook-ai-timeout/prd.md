# Configure webhook AI normalization timeout

## Goal

Allow webhook AI normalization to tolerate slow upstream model responses while keeping the timeout controlled by application configuration.

## Requirements

- Replace the hard-coded 2-second model normalization timeout with a configurable duration.
- Use 60 seconds as the default/configured production value.
- Thread the configured timeout through the existing analyzer construction path without changing the model request contract.
- Preserve deterministic fallback behavior when the model call times out or otherwise fails.
- Add or update focused tests for the default/configured timeout and timeout fallback behavior.

## Acceptance Criteria

- [x] A webhook AI normalization request is bounded by the configured timeout, defaulting to 60 seconds.
- [x] Existing callers remain compatible or are updated consistently with the new constructor/configuration path.
- [x] A model timeout returns the deterministic fallback result without failing webhook normalization.
- [x] Relevant backend tests pass.

## Out Of Scope

- Changing model prompts, retry policy, provider selection, or fingerprint semantics.
- Removing the timeout bound entirely.

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
