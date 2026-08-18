# Use side-by-side code diff in prototype

## Goal

Replace the unified patch text in the incident remediation prototype with a
VS Code-style side-by-side comparison so reviewers can compare the pinned
production baseline and proposed hotfix line by line.

## Confirmed Facts

- The affected view is `frontend/src/App.tsx`, where `RemediationPanel`
  conditionally renders `remediationRationale.diff` inside a single `<pre>`.
- The fixture in `frontend/src/data.ts` contains the baseline and proposed
  lines for `internal/validator/locale.go`.
- Playwright already opens the remediation view and expands the patch diff in
  `frontend/tests/prototype.spec.ts`.

## Requirements

- Present the patch as adjacent original and modified code panes, labelled
  with their baseline and proposed revisions.
- Support reviewing every changed file in the remediation by showing a file
  list and switching the adjacent code panes to the selected file.
- Preserve the code's fixed-width formatting and display line numbers.
- Mark removed source lines in the original pane and added source lines in the
  modified pane, while retaining unchanged context on both sides.
- Keep the existing expand/collapse control and verification content working.
- On narrow screens, stack the panes vertically while preserving readable code
  and horizontal scrolling within each pane where needed.

## Acceptance Criteria

- [x] Expanding the patch displays distinct Original and Modified code panes
      rather than a unified diff block.
- [x] The left pane includes the original direct lookup; the right pane
      includes the fallback guard and default-locale return.
- [x] Changed lines have clear deletion/addition visual treatment and both
      panes expose line numbers.
- [x] The prototype exposes more than one changed file and selecting a file
      updates both code panes to that file's comparison.
- [x] The existing desktop workflow test continues to pass and asserts the
      side-by-side view.
- [x] Production build and Playwright tests pass.

## Out Of Scope

- Adding a general-purpose diff library or making the prototype's fixture data
  dynamically editable.
- Changing the proposed hotfix or remediation decision flow.

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
