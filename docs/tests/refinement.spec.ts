import { expect, test } from "@playwright/test";

for (const locale of ["en", "zh-cn"]) {
  test(`${locale} whole homepage reflows at narrow, tablet, and wide sizes`, async ({
    page,
  }) => {
    for (const width of [320, 390, 768, 1440, 1920]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(locale === "en" ? "/" : "/zh-cn/");
      await expect(page.locator("h1")).toBeVisible();
      await expect(
        page.locator("site-search button[data-open-modal]"),
      ).toBeVisible();
      await expect(page.locator("starlight-theme-select select")).toBeVisible();
      await expect(page.locator("starlight-lang-select select")).toBeVisible();
      await expect(page.locator(".site-header .site-title")).toBeVisible();
      const dimensions = await page.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        viewport: document.documentElement.clientWidth,
        sections: [...document.querySelectorAll(".home-container")].map(
          (element) => element.getBoundingClientRect().width,
        ),
        header: [
          ...document.querySelectorAll(
            ".site-title, site-search button[data-open-modal], starlight-theme-select, starlight-lang-select",
          ),
        ].map((element) => element.getBoundingClientRect().toJSON()),
      }));
      expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.viewport);
      for (const section of dimensions.sections)
        expect(section).toBeLessThanOrEqual(1280);
      for (let i = 1; i < dimensions.header.length; i++) {
        expect(dimensions.header[i].left).toBeGreaterThanOrEqual(
          dimensions.header[i - 1].right - 1,
        );
      }
      await expect(page.locator(".home-footer-bottom a")).toBeVisible();
    }
  });
}

test("homepage theme and language controls share a compact, contained focus style", async ({
  page,
}) => {
  await page.setViewportSize({ width: 768, height: 900 });
  await page.goto("/zh-cn/");

  const controls = page.locator(
    ".site-header .right-group > :is(starlight-theme-select, starlight-lang-select)",
  );
  await expect(controls).toHaveCount(2);

  const restingStyles = await controls.evaluateAll((elements) =>
    elements.map((element) => {
      const label = element.querySelector("label")!;
      const styles = getComputedStyle(label);
      return {
        height: label.getBoundingClientRect().height,
        borderStyle: styles.borderStyle,
        borderColor: styles.borderColor,
        backgroundColor: styles.backgroundColor,
      };
    }),
  );
  expect(restingStyles[0]).toEqual(restingStyles[1]);
  expect(restingStyles[0]?.height).toBe(36);
  expect(restingStyles[0]?.borderStyle).toBe("solid");
  expect(restingStyles[0]?.borderColor).toBe("rgba(0, 0, 0, 0)");
  expect(restingStyles[0]?.backgroundColor).toBe("rgba(0, 0, 0, 0)");

  const themeSelect = controls.first().locator("select");
  await themeSelect.focus();
  await expect(themeSelect).toBeFocused();
  await page.waitForTimeout(200);

  const focusedStyles = await controls.first().evaluate((element) => {
    const label = element.querySelector("label")!;
    const select = element.querySelector("select")!;
    return {
      labelBorderColor: getComputedStyle(label).borderColor,
      labelBoxShadow: getComputedStyle(label).boxShadow,
      selectOutlineStyle: getComputedStyle(select).outlineStyle,
    };
  });
  expect(focusedStyles.labelBorderColor).not.toBe(
    restingStyles[0]?.borderColor,
  );
  expect(focusedStyles.labelBoxShadow).not.toBe("none");
  expect(focusedStyles.selectOutlineStyle).toBe("none");
});

test("homepage supports reduced motion and 200 percent zoom", async ({
  page,
}) => {
  await page.setViewportSize({ width: 640, height: 900 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/zh-cn/");
  await page.evaluate(() => {
    document.documentElement.style.zoom = "2";
  });
  const layout = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    reducedMotion: matchMedia("(prefers-reduced-motion: reduce)").matches,
    transitionDuration: parseFloat(
      getComputedStyle(document.querySelector(".home-button")!)
        .transitionDuration,
    ),
  }));
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth);
  expect(layout.reducedMotion).toBe(true);
  expect(layout.transitionDuration).toBeLessThanOrEqual(0.001);
  await expect(page.locator(".home-future-flow")).toBeVisible();
  await expect(page.locator(".home-footer-bottom a")).toBeVisible();
});
