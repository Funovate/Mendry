import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const journeys = [
  {
    locale: "en",
    prefix: "",
    headings: [
      "Evaluate your first production incident",
      "Prerequisites",
      "Installation status",
      "Bootstrap the administrator",
      "Configure your first incident response project",
      "Review your first production incident",
    ],
    paths: [
      "/docs/get-started/",
      "/docs/get-started/prerequisites/",
      "/docs/get-started/install/",
      "/docs/get-started/bootstrap/",
      "/docs/get-started/first-project/",
      "/docs/get-started/first-incident/",
    ],
  },
  {
    locale: "zh-CN",
    prefix: "/zh-cn",
    headings: [
      "评估第一个生产问题",
      "前提条件",
      "安装状态",
      "初始化管理员",
      "创建首个项目",
      "审阅首个事故",
    ],
    paths: [
      "/zh-cn/docs/get-started/",
      "/zh-cn/docs/get-started/prerequisites/",
      "/zh-cn/docs/get-started/install/",
      "/zh-cn/docs/get-started/bootstrap/",
      "/zh-cn/docs/get-started/first-project/",
      "/zh-cn/docs/get-started/first-incident/",
    ],
  },
];

for (const journey of journeys) {
  test(`${journey.locale} operator journey renders in order`, async ({
    page,
  }) => {
    for (const [index, path] of journey.paths.entries()) {
      await page.goto(path);
      await expect(page.locator("html")).toHaveAttribute(
        "lang",
        journey.locale,
      );
      await expect(
        page.getByRole("heading", { level: 1, name: journey.headings[index] }),
      ).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(page.viewportSize()!.width);
    }
  });

  test(`${journey.locale} installation is a non-executable planned page`, async ({
    page,
  }) => {
    await page.goto(`${journey.prefix}/docs/get-started/install/`);
    await expect(page.locator("main")).toContainText("planned");
    await expect(page.locator("main pre")).toHaveCount(0);
  });
}

const docsCases = [
  {
    locale: "en",
    path: "/docs/guides/extending-harness/",
    heading: "Extend the Agent Harness",
    searchPath: "/docs/",
    searchButton: "Search",
    query: "Harness",
    result: "Agent Harness for bounded AI execution",
  },
  {
    locale: "zh-CN",
    path: "/zh-cn/docs/guides/extending-harness/",
    heading: "扩展 Agent Harness",
    searchPath: "/zh-cn/docs/",
    searchButton: "搜索",
    query: "Harness",
    result: "Agent Harness",
  },
  {
    locale: "en",
    path: "/docs/guides/signed-webhooks/",
    heading: "Signed webhooks for incident ingestion",
    searchPath: "/docs/",
    searchButton: "Search",
    query: "webhook",
    result: "Signed webhooks for incident ingestion",
  },
  {
    locale: "zh-CN",
    path: "/zh-cn/docs/guides/signed-webhooks/",
    heading: "签名 Webhook",
    searchPath: "/zh-cn/docs/",
    searchButton: "搜索",
    query: "事故",
    result: "审阅首个事故",
  },
];

for (const pageCase of docsCases) {
  for (const theme of ["light", "dark"]) {
    test(`${pageCase.locale} ${pageCase.heading} ${theme} operator page is accessible`, async ({
      page,
    }) => {
      await page.goto(pageCase.path);
      await page
        .locator("starlight-theme-select select:visible")
        .first()
        .selectOption(theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(
        page.getByRole("heading", { level: 1, name: pageCase.heading }),
      ).toBeVisible();
      const accessibility = await new AxeBuilder({ page }).analyze();
      expect(accessibility.violations).toEqual([]);
    });
  }

  test(`${pageCase.locale} ${pageCase.heading} Pagefind returns a localized result`, async ({
    page,
  }) => {
    test.skip(test.info().project.name !== "desktop", "search is covered once");
    await page.goto(pageCase.searchPath);
    const openSearch = page.getByRole("button", {
      name: pageCase.searchButton,
    });
    await expect(openSearch).toBeEnabled();
    await openSearch.click();
    const input = page.locator(".pagefind-ui__search-input");
    await expect(input).toBeVisible();
    await input.fill(pageCase.query);
    await expect(
      page
        .locator(".pagefind-ui__result-link", { hasText: pageCase.result })
        .first(),
    ).toBeVisible();
  });
}

test("conventional Chinese docs reflow at 320px and 200% CSS zoom", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto("/zh-cn/docs/reference/configuration/");
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(320);

  await page.evaluate(() => {
    document.documentElement.style.zoom = "2";
  });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(320);
  await expect(
    page.getByRole("heading", { level: 1, name: "配置参考" }),
  ).toBeVisible();
});
