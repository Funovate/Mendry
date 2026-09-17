import { expect, test } from "@playwright/test";

for (const reducedMotion of ["no-preference", "reduce"] as const) {
  test(`navigation to docs respects motion preference: ${reducedMotion}`, async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion });
    await page.addInitScript(() => {
      window.addEventListener("pagereveal", (event) => {
        sessionStorage.setItem(
          "navigation-transition",
          String(
            Boolean(
              (event as PageTransitionEvent & { viewTransition?: unknown })
                .viewTransition,
            ),
          ),
        );
      });
    });
    for (const source of [
      "/",
      "/missing-page/",
      "/zh-cn/",
      "/zh-cn/missing-page/",
    ]) {
      await page.goto(source);
      await page
        .locator(
          source.includes("missing")
            ? ".not-found:not([hidden]) .secondary"
            : ".home-button-primary",
        )
        .click();
      await expect(page).toHaveURL(/\/docs\//);
      await expect(page.locator("h1")).toBeVisible();
      const transitionResult = await page.evaluate(() =>
        sessionStorage.getItem("navigation-transition"),
      );
      if (reducedMotion === "reduce") {
        expect(transitionResult).toBe("false");
      } else {
        expect(["true", "false"]).toContain(transitionResult);
        expect(
          await page.evaluate(async () => {
            const stylesheets = Array.from(
              document.querySelectorAll<HTMLLinkElement>(
                'link[rel="stylesheet"][href]',
              ),
            );
            const css = await Promise.all(
              stylesheets.map(async (stylesheet) =>
                (await fetch(stylesheet.href)).text(),
              ),
            );
            return css.some((text) =>
              /@view-transition\s*\{\s*navigation\s*:\s*auto\s*\}/.test(text),
            );
          }),
        ).toBe(true);
      }
      await page
        .locator(".site-header starlight-theme-select select")
        .selectOption("dark");
      await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
      await page.locator(".site-header button[data-open-modal]").click();
      await expect(page.locator("site-search dialog")).toBeVisible();
      await page.keyboard.press("Escape");
    }
  });
}
