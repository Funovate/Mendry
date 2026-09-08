import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const pages = [
  { path: "/", lang: "en", heading: "FixThe", localeLink: /简体中文/ },
  { path: "/zh-cn/", lang: "zh-CN", heading: "FixThe", localeLink: /English/ },
];

for (const pageCase of pages) {
  test(`${pageCase.lang} introduction is usable and accessible`, async ({
    page,
  }, testInfo) => {
    await page.goto(pageCase.path);
    await expect(page.locator("html")).toHaveAttribute("lang", pageCase.lang);
    await expect(
      page.getByRole("heading", { level: 1, name: pageCase.heading }),
    ).toBeVisible();
    await expect(page.getByRole("main")).toBeVisible();
    await expect(page.locator(".intro-product-frame img")).toHaveJSProperty(
      "complete",
      true,
    );
    expect(
      await page
        .locator(".intro-product-frame img")
        .evaluate((image: HTMLImageElement) => image.naturalWidth),
    ).toBeGreaterThan(0);
    await expect(page.locator(".intro-workflow")).toBeVisible();
    await expect(page.locator("starlight-theme-select select")).toBeVisible();

    const mediaTop = await page
      .locator(".intro-product-frame")
      .evaluate((element) => {
        return element.getBoundingClientRect().top;
      });
    expect(mediaTop).toBeLessThan(page.viewportSize()?.height ?? 0);

    const accessibility = await new AxeBuilder({ page }).analyze();
    expect(accessibility.violations).toEqual([]);

    const documentWidth = await page.evaluate(
      () => document.documentElement.scrollWidth,
    );
    const viewportWidth = page.viewportSize()?.width ?? 0;
    expect(documentWidth).toBeLessThanOrEqual(viewportWidth);

    await testInfo.attach("introduction", {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    });
  });
}

test("locale switch preserves the introduction destination", async ({
  page,
}) => {
  await page.goto("/");
  const localeSelect = page.locator("starlight-lang-select select");
  await expect(localeSelect).toBeVisible();
  await localeSelect.selectOption("/zh-cn/");
  await expect(page).toHaveURL(/\/zh-cn\/$/);
});

test("preview pages remain non-indexable", async ({ page }) => {
  await page.goto("/docs/");
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute(
    "content",
    "noindex, nofollow",
  );
  await expect(page.locator('link[rel="canonical"]')).toHaveCount(0);
});

test("unknown routes use the localized 404 surface", async ({ page }) => {
  const response = await page.goto("/does-not-exist/");
  expect(response?.status()).toBe(404);
  await expect(
    page.getByRole("heading", { name: "Page not found", exact: true }),
  ).toBeVisible();

  const chineseResponse = await page.goto("/zh-cn/does-not-exist/");
  expect(chineseResponse?.status()).toBe(404);
  await expect(
    page.getByRole("heading", { name: "页面不存在", exact: true }),
  ).toBeVisible();
});

test("Simplified Chinese introduction reflows with reduced motion", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/zh-cn/");

  expect(
    await page.evaluate(
      () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    ),
  ).toBe(true);

  const layout = await page.evaluate(() => {
    const action = document.querySelector<HTMLElement>(".intro-action");
    return {
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      scrollBehavior: getComputedStyle(document.documentElement).scrollBehavior,
      transitionDuration: action
        ? Number.parseFloat(getComputedStyle(action).transitionDuration)
        : Number.NaN,
    };
  });

  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth);
  expect(layout.scrollBehavior).toBe("auto");
  expect(layout.transitionDuration).toBeLessThanOrEqual(0.001);
});
