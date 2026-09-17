import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const pages = [
  {
    path: "/",
    lang: "en",
    heading: "From production alertto a fix ready for review.",
    primaryHref: "/docs/get-started/",
    example: "ILLUSTRATIVE EXAMPLE",
    removedHeading: "From the first alert",
    current: "INVESTIGATION · DEFAULT",
    hotfix: "AUTOMATIC HOTFIX · OPT-IN",
    handoff: "Your team takes it from here",
    optionalValidation: "Prevalidate locally · optional",
    boundary:
      "Mendry does not automatically merge, deploy, roll back, or confirm production recovery.",
  },
  {
    path: "/zh-cn/",
    lang: "zh-CN",
    heading: "从生产告警，到可审查的修复。",
    primaryHref: "/zh-cn/docs/get-started/",
    example: "流程示例",
    removedHeading: "从告警到修复方案",
    current: "问题调查 · 默认启用",
    hotfix: "自动 HOTFIX · 按项目启用",
    handoff: "由你的团队接续",
    optionalValidation: "本地预验证 · 可选",
    boundary: "Mendry 不自动合并、部署、回滚或确认生产恢复。",
  },
];

for (const pageCase of pages) {
  for (const theme of ["light", "dark"]) {
    test(`${pageCase.lang} ${theme} presents investigation, opt-in hotfix, and team handoff`, async ({
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
        page.locator(".home-investigation-flow .home-flow-label"),
      ).toHaveText(pageCase.current);
      await expect(
        page.locator(".home-hotfix-flow .home-flow-label"),
      ).toHaveText(pageCase.hotfix);
      await expect(page.locator(".home-investigation-flow li")).toHaveCount(3);
      await expect(page.locator(".home-hotfix-flow li")).toHaveCount(3);
      await expect(page.locator(".home-hotfix-flow")).toContainText(
        pageCase.optionalValidation,
      );
      await expect(page.locator(".home-handoff h3")).toHaveText(
        pageCase.handoff,
      );
      await expect(page.locator(".home-handoff")).toContainText(
        pageCase.boundary,
      );
      await expect(page.locator(".home-delivery-strip li")).toHaveCount(3);
      await expect(page.locator(".home-workspace-paths a")).toHaveCount(3);
      await expect(page.locator(".home-stack")).toBeVisible();
      await expect(page.locator(".home-paths a")).toHaveCount(3);
      await expect(page.locator(".home-footer")).toBeVisible();

      const layout = await page.evaluate(() => {
        const sections = [
          ".home-hero",
          ".home-investigation",
          ".home-workflow",
          ".home-workspace",
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
          heroCopy: document
            .querySelector(".home-hero-copy")!
            .getBoundingClientRect()
            .toJSON(),
          brandScene: document
            .querySelector(".home-brand-scene")!
            .getBoundingClientRect()
            .toJSON(),
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
        expect(layout.heroCopy.bottom).toBeLessThanOrEqual(
          layout.brandScene.top,
        );
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
    test(`${pageCase.lang} explains current capabilities and team handoff without JavaScript`, async ({
      page,
    }) => {
      await page.goto(pageCase.path);
      await expect(page.locator("[data-execution-model]")).toBeVisible();
      await expect(page.locator("canvas")).toHaveCount(0);
      await expect(page.locator(".home-investigation-flow")).toContainText(
        pageCase.current,
      );
      await expect(page.locator(".home-hotfix-flow")).toContainText(
        pageCase.hotfix,
      );
      await expect(page.locator(".home-handoff")).toContainText(
        pageCase.boundary,
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
