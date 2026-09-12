import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const pages = [
  {
    path: "/",
    lang: "en",
    heading: "AI incident investigation.From alert to repair proposal.",
    primaryHref: "/docs/get-started/",
    example: "ILLUSTRATIVE EXAMPLE",
    removedHeading: "From the first alert",
    current: "CURRENT SCOPE · PREVIEW",
    future: "EVOLUTION · PLANNED",
    recovery: "Verify recovery & close",
  },
  {
    path: "/zh-cn/",
    lang: "zh-CN",
    heading: "从生产告警，走向修复闭环。",
    primaryHref: "/zh-cn/docs/get-started/",
    example: "调查示例",
    removedHeading: "从告警到修复方案",
    current: "当前覆盖 · 预览",
    future: "演进方向 · 规划中",
    recovery: "确认恢复，关闭问题",
  },
];

for (const pageCase of pages) {
  for (const theme of ["light", "dark"]) {
    test(`${pageCase.lang} ${theme} presents the full incident-to-recovery story`, async ({
      page,
    }, testInfo) => {
      await page.goto(pageCase.path);
      await page.locator("starlight-theme-select select").selectOption(theme);
      await expect(page.locator("html")).toHaveAttribute("lang", pageCase.lang);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(page.locator("h1")).toHaveText(pageCase.heading);
      await expect(page.locator(".home-button-primary")).toHaveAttribute(
        "href",
        pageCase.primaryHref,
      );
      await expect(page.locator(".site-header .site-title")).toBeVisible();
      await expect(page.locator(".home-brand-scene")).toBeVisible();
      await expect(page.locator(".home-example")).toHaveText(pageCase.example);
      await expect(
        page.getByRole("heading", { name: pageCase.removedHeading }),
      ).toHaveCount(0);
      await expect(page.locator(".home-evidence-column")).toHaveCount(3);
      await expect(page.locator(".home-stage-label > span")).toHaveCount(0);
      await expect(page.locator("#demo-note")).toBeVisible();
      await expect(
        page.locator(".home-current-flow .home-flow-label"),
      ).toHaveText(pageCase.current);
      await expect(
        page.locator(".home-future-flow .home-flow-label"),
      ).toHaveText(pageCase.future);
      await expect(page.locator(".home-current-stages li")).toHaveCount(3);
      await expect(page.locator(".home-future-stages li")).toHaveCount(4);
      await expect(page.locator(".home-recovery strong")).toHaveText(
        pageCase.recovery,
      );
      await expect(page.locator(".home-stack")).toBeVisible();
      await expect(page.locator(".home-paths a")).toHaveCount(3);
      await expect(page.locator(".home-footer")).toBeVisible();

      const layout = await page.evaluate(() => {
        const sections = [
          ".home-hero",
          ".home-investigation",
          ".home-workflow",
          ".home-integration",
          ".home-start",
          ".home-footer",
        ];
        return {
          boxes: sections.map((selector) =>
            document.querySelector(selector)!.getBoundingClientRect().toJSON(),
          ),
          proofItems: [
            ...document.querySelectorAll(".home-proof-strip li"),
          ].map((item) => item.getBoundingClientRect().toJSON()),
          consoleBrand: {
            whiteSpace: getComputedStyle(
              document.querySelector(".home-console-brand")!,
            ).whiteSpace,
            contextDisplay: getComputedStyle(
              document.querySelector(".home-console-brand > span:last-child")!,
            ).display,
          },
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          externalResources: performance
            .getEntriesByType("resource")
            .map((entry) => entry.name)
            .filter(
              (url) =>
                /^https?:/.test(url) && new URL(url).origin !== location.origin,
            ),
        };
      });
      expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth);
      expect(layout.externalResources).toEqual([]);
      expect(layout.proofItems).toHaveLength(3);
      if (layout.clientWidth <= 768) {
        expect(layout.consoleBrand).toEqual({
          whiteSpace: "nowrap",
          contextDisplay: "none",
        });
        expect(layout.proofItems[1].top).toBeGreaterThan(
          layout.proofItems[0].bottom,
        );
        expect(layout.proofItems[2].top).toBeGreaterThan(
          layout.proofItems[1].bottom,
        );
      }
      for (let i = 1; i < layout.boxes.length; i++) {
        expect(layout.boxes[i].top).toBeGreaterThanOrEqual(
          layout.boxes[i - 1].bottom - 1,
        );
      }
      const accessibility = await new AxeBuilder({ page }).analyze();
      expect(accessibility.violations).toEqual([]);
      await testInfo.attach(`${pageCase.lang}-${theme}-full-homepage`, {
        body: await page.screenshot({ fullPage: true }),
        contentType: "image/png",
      });
    });
  }
}

test("homepage navigation, search, and locale switch remain usable", async ({
  page,
}) => {
  await page.goto("/");
  await page.locator(".home-button-secondary").click();
  await expect(page).toHaveURL(/#workflow$/);
  await page.keyboard.press("Control+k");
  await expect(page.locator("site-search dialog")).toBeVisible();
  await expect(page.locator("site-search input")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.locator("site-search dialog")).toBeHidden();
  await page.locator("starlight-lang-select select").selectOption("/zh-cn/");
  await expect(page).toHaveURL(/\/zh-cn\/#workflow$/);
  await page.locator(".home-footer-bottom a").click();
  await expect(page.locator("h1")).toBeInViewport();
});

test.describe("homepage static semantics", () => {
  test.use({ javaScriptEnabled: false });
  for (const pageCase of pages) {
    test(`${pageCase.lang} explains current capabilities and future direction without JavaScript`, async ({
      page,
    }) => {
      await page.goto(pageCase.path);
      await expect(page.locator("[data-execution-model]")).toBeVisible();
      await expect(page.locator("canvas")).toHaveCount(0);
      await expect(page.locator(".home-current-flow")).toContainText(
        pageCase.current,
      );
      await expect(page.locator(".home-future-flow")).toContainText(
        pageCase.future,
      );
      await expect(page.locator(".home-paths a")).toHaveCount(3);
      await page.locator(".home-button-primary").click();
      await expect(page).toHaveURL(new RegExp(`${pageCase.primaryHref}$`));
    });
  }
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
