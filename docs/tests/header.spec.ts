import { expect, test } from "@playwright/test";

for (const width of [320, 390, 768, 1440]) {
  test(`shared navigation stays consistent at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    for (const theme of ["light", "dark"]) {
      let baseline: unknown;
      for (const route of [
        "/",
        "/missing-page/",
        "/zh-cn/",
        "/zh-cn/missing-page/",
      ]) {
        await page.goto(route);
        const header = page.locator(".site-header");
        await header
          .locator("starlight-theme-select select")
          .selectOption(theme);
        await expect(
          header.locator("starlight-lang-select select"),
        ).toBeVisible();
        const dimensions = await header.evaluate((element) => {
          const controls = [
            ...element.querySelectorAll(
              ".site-title, button[data-open-modal], .right-group label",
            ),
          ];
          return {
            styles: controls.slice(1).map((control) => {
              const style = getComputedStyle(control);
              return {
                height: control.getBoundingClientRect().height,
                radius: style.borderRadius,
                font: style.fontSize,
              };
            }),
            bounds: controls.map((control) =>
              control.getBoundingClientRect().toJSON(),
            ),
            scroll: document.documentElement.scrollWidth,
            viewport: document.documentElement.clientWidth,
          };
        });
        baseline ??= dimensions.styles;
        expect(dimensions.styles).toEqual(baseline);
        expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.viewport);
        for (let i = 1; i < dimensions.bounds.length; i++) {
          expect(dimensions.bounds[i].left).toBeGreaterThanOrEqual(
            dimensions.bounds[i - 1].right - 1,
          );
        }
        await header.locator("button[data-open-modal]").click();
        await expect(page.locator("site-search dialog")).toBeVisible();
        await page.keyboard.press("Escape");
      }
    }
  });
}
