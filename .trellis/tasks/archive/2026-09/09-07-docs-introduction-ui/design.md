# Introduction UI Refinement Design

## Scope And Direction

Preserve the neutral/teal documentation aesthetic and the existing sequence:
introduction, workflow, evidence, authority, and readiness. Refine the shared
Astro introduction and its local styles without adding a framework or dependency.

## Presentation

- Tighten hero spacing and balance heading, summary, authority copy, and actions.
  Keep FixThe prominent and use fixed font-size steps at responsive breakpoints.
- Simplify decorative screenshot framing and shadow; retain the reviewed local
  product image without obscuring or cropping its content. Favor an unframed
  composition over the current simulated browser card. Confirm final dimensions
  against desktop and mobile screenshots during implementation.
- Reduce excessive section whitespace while retaining clear section boundaries.
  Align workflow steps, evidence rows, and authority definitions for scanning.
- Make primary and secondary actions visibly distinct with stable heights,
  wrapping labels, visible focus, and sufficient contrast in both themes.
- Check readiness-band colors explicitly: its current white text and
  theme-dependent `--fx-ink` background can become white on a light background
  in dark mode. Use semantic foreground/background pairs for the band.
- Keep the next section visibly entering the first viewport at representative
  desktop/mobile sizes; at short or zoomed viewports prioritize full content
  access and natural scrolling over clipping or tiny text.

## Localization And Boundaries

Move retained media captions and section labels into `IntroContent` with explicit
English and Chinese values. Preserve product claims, actions, URLs, metadata,
and reviewed image provenance. No changes to deployment or preview indexing.

Scope presentation selectors to the introduction wherever possible. Shared
Starlight controls and docs tokens remain compatible with conventional pages.

## Validation And Rollback

Reuse current checks and browser fixtures. Extend browser assertions only for
concrete uncovered behavior: localized captions, theme readability, actual
first-viewport section position, and zoom/reflow. Inspect screenshots in both
locales and themes, with a representative docs route for shared-style effects.

All changes stay in the introduction component/content/style/test surface.
Rollback only this task's individual edits, preserving pre-existing untracked
docs work. Run GitNexus impact before editing symbols; document index gaps and
use package checks for unindexed docs code.
