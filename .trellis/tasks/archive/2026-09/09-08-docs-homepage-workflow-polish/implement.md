# Implementation notes

## Workflow styling refinement

- Changed only workflow selectors in `docs/src/styles/starlight.css`; preserved all pre-existing edits in that file and the other dirty homepage files.
- Gave the workflow its own full-width section layout, with the heading and introductory sentence balanced above the process at wider widths.
- Added one quiet, lightly tinted shared surface with a thin border and 4px radius; no nested stage cards, gradients, icons, animation, or dependencies.
- Increased numbered nodes to 40px, separated connectors from their outlines, refined title weight and spacing, and increased description size and line height.
- Kept stages 01–03 on the accent token; stage 04 retains authority-colored number/title and adds a subtle authority tint and outer ring.
- Desktop uses a connected four-stage row. Tablet uses two connected pairs with every node aligned over its own title. At 390px and 320px, all four stages form a connected vertical list.
- Existing English/Chinese strings and semantic ordered-list markup are untouched. No function/class/method symbols were modified, so symbol impact analysis does not apply. GitNexus MCP tools are not exposed to this implement session.

## Verification

- `npm run lint`: passed after final CSS changes.
- `npm run check`: passed, zero Astro errors/warnings/hints; 38 operator routes and two introductions verified.
- `npm run test`: five passed.
- `npm run build`: passed after final CSS changes; 40 routes and 41 generated pages verified. Expected preview-mode sitemap warning only.
- Final focused Playwright run (`npx playwright test tests/introduction.spec.ts tests/refinement.spec.ts`): 32 passed after tablet alignment. Axe reports zero violations for both locales and themes; responsive and 200% CSS zoom overflow checks pass.
- `git diff --check`: passed.
- No tests were modified; existing homepage tests cover localized stage copy, 4/2/1 layouts, authority colors, both themes/locales, axe, 200% CSS zoom, reduced motion, and overflow.

## Screenshots

16 final section screenshots are saved in `screenshots/{en,zh-cn}-{light,dark}-{1280,768,390,320}.png` beside this note. Captured with reduced motion enabled. Reviewed representative desktop light/dark, Chinese desktop, tablet, and 320px English/Chinese images for spacing, node alignment, wrapping, and clipping. Tablet intentionally wraps into two connected pairs instead of moving nodes into the center gutter away from their labels.

No commit or archive operation was performed.
