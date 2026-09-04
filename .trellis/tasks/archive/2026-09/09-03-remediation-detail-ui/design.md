# Remediation detail UI design

## Architecture

Keep the existing production data flow and mutation behavior unchanged:

```text
api.ts review DTO -> RemediationPanel -> feature-owned layout/CSS
                                      -> unified diff view projection
```

Add a small feature-local diff projection module next to the incidents feature. It will parse the advisory unified diff string into bounded view rows with an original and modified side. It is presentation-only: no API DTO changes, no patch application, and no mutation of server state.

## Layout

`RemediationPanel` will use these visual regions in this order:

1. Review heading and existing action/error states.
2. Compact run facts grid containing status, risk, generation, attempt, origin, terminal reason, deployed commit, and loop mode.
3. Full-width recovery/manual-review projection when the existing state predicates allow it.
4. Two-column review body on wide desktop:
   - main: Diagnosis at a comfortable reading width;
   - secondary: bounded Attempts history.
5. Full-width Plans section, with each plan split into rationale and implementation details on wide screens.
6. Full-width Suggested diff viewer below the plans.

The incident shell retains both the application sidebar and incident list on common desktop widths, so the review body collapses to one column at `1400px` viewport width. The layout collapses fully to one column at the mobile breakpoint. Prose blocks use a readable max width, while identifiers and file lists wrap inside their region.

## Diff projection

Parse standard unified diff markers:

- file headers identify the displayed path;
- hunk headers are retained as context rows;
- context lines render on both sides;
- adjacent removals and additions are paired row-by-row so old and new code can be compared horizontally;
- unmatched removals/additions render an empty cell on the opposite side;
- the parser preserves line numbers where supplied by hunk headers;
- if a diff cannot be parsed into a file, render the original text in the existing bounded fallback code block rather than dropping content.

Desktop renders each file as two aligned panes labelled `Original` and `Changed`. Each pane keeps `white-space: pre` and its own horizontal overflow. At narrow widths the panes stack vertically, retaining code readability instead of shrinking or wrapping source lines.

## Styling

Use existing tokens and `var(--radius-panel)`/`var(--radius-control)` values. Add only remediation feature selectors in `incidents.css`. Use subtle borders, an ink hierarchy, compact metadata, and restrained state colors. Review regions may be framed, but repeated plan and attempt entries use dividers rather than nested card styling. Avoid full-width grey note bars for every paragraph; use neutral text for explanatory copy and framed blocks only for meaningful review units.

## Compatibility

- Existing loading, no-run, error, recovery, blocked-manual-review, diagnosis, plan, and retry states stay behaviorally identical.
- Existing capability checks and query keys remain untouched.
- Current E2E text assertions continue to find the diagnosis and diff content; add parser unit coverage and focused structure assertions as needed.
- No backend migration or route change is required.

## Risks and mitigations

- Unified diff formats can contain multiple files and uneven change blocks. Cover context, deletion, addition, pairing, multiple files, and fallback in pure tests.
- Long generated text can still overflow if a descendant has a minimum width. Apply `min-width: 0`, `overflow-wrap: anywhere`, and explicit code-pane overflow at every grid boundary.
- Changing markup can affect existing accessibility queries. Keep real headings, button names, time elements, and recovery roles unchanged.
