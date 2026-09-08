import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const pages = [
  {
    path: "/",
    lang: "en",
    heading: "FixThe",
    localeLink: /简体中文/,
    stages: [
      "01 Signal & evidence",
      "02 Investigation & association",
      "03 Plan selection",
      "04 Human review",
    ],
    stageListLabel: "Incident investigation stages",
    topLine: "Evidence-driven · human-controlled",
    description: "Four-stage incident investigation workflow",
    fallbackStatus: "Merge and deployment remain human-controlled",
    caption: "From signal to review",
    captionDetail: "Traceable evidence · bounded authority",
    phaseStatus: [
      "Alerts, logs, and code preserve their evidence sources",
      "Match clues across traces and code",
      "Compare candidate plans; mark a recommendation",
      "Stops at human review · no automatic merge or deployment",
    ],
    fallbackWidth: 1240,
    fallbackHeight: 815,
    mediaAlt:
      "FixThe four-stage incident investigation workflow illustration showing evidence, investigation, plan selection, and human review",
    pauseLabel: "Pause animation",
    playLabel: "Play animation",
    staticLabel: "Static workflow",
    summary:
      "Investigate production incidents with bounded evidence, durable reasoning, and an explicit path to human review.",
    authority:
      "Deterministic operation does not require a remote LLM. Optional remote analysis receives redacted context; merge and production deployment remain human-controlled.",
    workflowHeading: "From signal to review",
  },
  {
    path: "/zh-cn/",
    lang: "zh-CN",
    heading: "FixThe",
    localeLink: /English/,
    stages: ["01 信号与证据", "02 调查与关联", "03 方案选取", "04 人工审查"],
    stageListLabel: "事故调查阶段",
    topLine: "证据驱动 · 人工把关",
    description: "四阶段事故调查流程示意",
    fallbackStatus: "合并与部署，由人决定",
    caption: "从信号到审查",
    captionDetail: "证据可追溯 · 权限有边界",
    phaseStatus: [
      "告警、日志与代码，保留证据来源",
      "匹配线索，关联调用与代码",
      "比较候选方案，标记推荐",
      "止于人工审查 · 不自动合并或部署",
    ],
    fallbackWidth: 1240,
    fallbackHeight: 815,
    mediaAlt:
      "FixThe 四阶段事故调查流程示意图，展示证据、调查、方案选取和人工审查",
    pauseLabel: "暂停动画",
    playLabel: "播放动画",
    staticLabel: "静态流程",
    summary: "通过受限证据、可持久化推理和明确的人工审查路径调查生产事故。",
    authority:
      "确定性运行不依赖远程大模型。可选的远程分析只接收脱敏上下文；合并与生产部署始终由人控制。",
    workflowHeading: "从信号到审查",
  },
];

for (const pageCase of pages) {
  test(`${pageCase.lang} introduction is usable and accessible`, async ({
    page,
  }, testInfo) => {
    await page.goto(pageCase.path);
    expect(
      await page.evaluate(() =>
        performance
          .getEntriesByType("resource")
          .map((entry) => entry.name)
          .filter((url) => /^https?:/.test(url))
          .filter((url) => new URL(url).origin !== location.origin),
      ),
    ).toEqual([]);
    await expect(page.locator("html")).toHaveAttribute("lang", pageCase.lang);
    await expect(
      page.getByRole("heading", { level: 1, name: pageCase.heading }),
    ).toBeVisible();
    await expect(page.getByRole("main")).toBeVisible();
    await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
      "data-hero-flow",
      "",
    );
    await expect(
      page.locator(".intro-flow-visual .scene-fallback"),
    ).toHaveAttribute("src", "/media/hero-flow/scene-fallback.png");
    await expect(
      page.locator(".intro-flow-visual .scene-fallback"),
    ).toHaveAttribute("width", pageCase.fallbackWidth.toString());
    await expect(
      page.locator(".intro-flow-visual .scene-fallback"),
    ).toHaveAttribute("height", pageCase.fallbackHeight.toString());
    await expect(
      page.locator(".intro-flow-visual .scene-fallback"),
    ).toHaveJSProperty("complete", true);
    const fallbackDimensions = await page
      .locator(".intro-flow-visual .scene-fallback")
      .evaluate((image: HTMLImageElement) => ({
        naturalWidth: image.naturalWidth,
        naturalHeight: image.naturalHeight,
      }));
    expect(fallbackDimensions).toEqual({
      naturalWidth: pageCase.fallbackWidth,
      naturalHeight: pageCase.fallbackHeight,
    });
    await expect(
      page.locator(".intro-flow-visual .scene-fallback"),
    ).toHaveAttribute("alt", pageCase.mediaAlt);
    await expect(page.locator(".scene-labels")).toHaveAttribute(
      "aria-label",
      pageCase.stageListLabel,
    );
    await expect(page.locator(".intro-flow-topline")).toContainText(
      pageCase.topLine,
    );
    await expect(page.locator("#hero-flow-description")).toContainText(
      pageCase.description,
    );
    await expect(page.locator(".scene-labels .stage")).toHaveCount(4);
    await expect(page.locator(".scene-labels .stage")).toHaveText(
      pageCase.stages,
    );
    await expect(page.locator(".intro-flow-caption")).toContainText(
      pageCase.caption,
    );
    await expect(page.locator(".intro-flow-caption")).toContainText(
      pageCase.captionDetail,
    );
    await expect(page.locator(".scene-status")).toBeVisible();
    await expect(page.locator(".intro-hero-copy")).toBeVisible();
    await expect(page.locator(".intro-summary")).toHaveText(pageCase.summary);
    await expect(page.locator(".intro-authority")).toHaveText(
      pageCase.authority,
    );
    await expect(page.locator(".intro-action-secondary")).toHaveAttribute(
      "href",
      pageCase.lang === "en"
        ? "/docs/concepts/architecture/"
        : "/zh-cn/docs/concepts/architecture/",
    );
    await expect(page.locator(".intro-workflow h2")).toHaveText(
      pageCase.workflowHeading,
    );
    await expect(
      page.locator(".intro-workflow > .intro-flow > li"),
    ).toHaveCount(4);
    await expect(page.locator("starlight-theme-select select")).toBeVisible();
    await page.waitForFunction(() => {
      const renderer =
        document.querySelector<HTMLElement>("[data-hero-flow]")?.dataset
          .renderer;
      return renderer === "webgl" || renderer === "fallback";
    });
    const renderer = await page
      .locator("[data-hero-flow]")
      .getAttribute("data-renderer");
    if (renderer === "webgl") {
      const control = page.locator(".motion-control");
      await expect(control).toBeVisible();
      await expect(control).toHaveAttribute("aria-label", pageCase.pauseLabel);
      await expect(control.locator(".motion-label")).toHaveText(
        pageCase.pauseLabel,
      );
      const canvasMetrics = await page
        .locator(".scene-canvas")
        .evaluate((canvas: HTMLCanvasElement) => ({
          width: canvas.width,
          height: canvas.height,
          cssWidth: canvas.clientWidth,
          cssHeight: canvas.clientHeight,
        }));
      expect(canvasMetrics.width).toBeGreaterThan(0);
      expect(canvasMetrics.height).toBeGreaterThan(0);
      expect(canvasMetrics.width).toBeLessThanOrEqual(
        canvasMetrics.cssWidth * 2 + 1,
      );
      expect(canvasMetrics.height).toBeLessThanOrEqual(
        canvasMetrics.cssHeight * 2 + 1,
      );
      expect(
        Number(
          await page.locator("[data-hero-flow]").getAttribute("data-meshes"),
        ),
      ).toBeGreaterThan(100);
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-connectors",
        "3",
      );
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-output",
        "none",
      );
      await page.waitForFunction(
        () =>
          document.querySelector<HTMLElement>("[data-hero-flow]")?.dataset
            .running === "true",
      );
      await control.click();
      await expect(control).toHaveAttribute("data-paused", "true");
      await expect(control).toHaveAttribute("aria-label", pageCase.playLabel);
      await expect(control.locator(".motion-label")).toHaveText(
        pageCase.playLabel,
      );
      const paused = await page
        .locator("[data-hero-flow]")
        .evaluate((element) => ({
          elapsed: element.getAttribute("data-elapsed"),
          frames: element.getAttribute("data-frames"),
        }));
      await page.waitForTimeout(150);
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-running",
        "false",
      );
      expect(
        await page.locator("[data-hero-flow]").evaluate((element) => ({
          elapsed: element.getAttribute("data-elapsed"),
          frames: element.getAttribute("data-frames"),
        })),
      ).toEqual(paused);
      await control.click();
      await expect(control).toHaveAttribute("data-paused", "false");
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-running",
        "true",
      );

      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-running",
        "false",
      );
      await page.evaluate(() => window.scrollTo(0, 0));
      await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
        "data-running",
        "true",
      );
    } else {
      await expect(page.locator(".scene-fallback")).toBeVisible();
      await expect(page.locator(".motion-control")).toBeHidden();
      await expect(page.locator(".scene-status")).toHaveText(
        pageCase.fallbackStatus,
      );
    }
    if (renderer === "webgl") {
      expect(
        JSON.parse(
          (await page
            .locator("[data-hero-flow]")
            .getAttribute("data-phase-status")) || "[]",
        ),
      ).toEqual(pageCase.phaseStatus);
    }
    await expect(page.locator(".intro-summary")).toBeVisible();
    await expect(page.locator(".intro-authority")).toBeVisible();
    await expect(page.locator(".intro-action-primary").first()).toHaveAttribute(
      "href",
      pageCase.lang === "en"
        ? "/docs/get-started/"
        : "/zh-cn/docs/get-started/",
    );
    await expect(page.locator(".intro-workflow .intro-kicker")).toBeVisible();
    const heroAndWorkflow = await page.evaluate(() => {
      const hero = document
        .querySelector(".intro-hero")
        ?.getBoundingClientRect();
      const workflow = document
        .querySelector(".intro-workflow")
        ?.getBoundingClientRect();
      return hero && workflow
        ? { heroBottom: hero.bottom, workflowTop: workflow.top }
        : null;
    });
    expect(heroAndWorkflow).not.toBeNull();
    expect(heroAndWorkflow!.workflowTop).toBeGreaterThanOrEqual(
      heroAndWorkflow!.heroBottom,
    );

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

test.describe("hero-flow fallback contracts", () => {
  test.use({ javaScriptEnabled: false });

  for (const pageCase of pages) {
    test(`${pageCase.lang} keeps the localized media fallback usable without JavaScript`, async ({
      page,
    }) => {
      await page.goto(pageCase.path);
      await expect(page.locator(".scene-fallback")).toBeVisible();
      await expect(page.locator(".scene-fallback")).toHaveAttribute(
        "alt",
        pageCase.mediaAlt,
      );
      await expect(page.locator(".scene-fallback")).toHaveAttribute(
        "width",
        pageCase.fallbackWidth.toString(),
      );
      await expect(page.locator(".scene-fallback")).toHaveAttribute(
        "height",
        pageCase.fallbackHeight.toString(),
      );
      await expect(page.locator(".scene-labels .stage")).toHaveCount(4);
      await expect(page.locator(".motion-control")).toBeHidden();
      await expect(page.locator(".scene-status")).toHaveText(
        pageCase.fallbackStatus,
      );
    });
  }
});

test("unavailable WebGL keeps the local fallback and hides motion control", async ({
  page,
}) => {
  await page.addInitScript(() => {
    HTMLCanvasElement.prototype.getContext = function () {
      return null;
    };
  });
  await page.goto("/");
  await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
    "data-renderer",
    "fallback",
  );
  await expect(page.locator(".scene-fallback")).toBeVisible();
  await expect(page.locator(".motion-control")).toBeHidden();
  await expect(page.locator(".scene-status")).toHaveText(
    "Merge and deployment remain human-controlled",
  );
});

test("reduced motion renders the completed human-review state", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/zh-cn/");
  await page.waitForFunction(() => {
    const renderer =
      document.querySelector<HTMLElement>("[data-hero-flow]")?.dataset.renderer;
    return renderer === "webgl" || renderer === "fallback";
  });
  if (
    (await page.locator("[data-hero-flow]").getAttribute("data-renderer")) ===
    "webgl"
  ) {
    await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
      "data-phase",
      "3",
    );
    await expect(page.locator(".scene-status")).toHaveText(
      "止于人工审查 · 不自动合并或部署",
    );
    await expect(page.locator(".motion-control")).toBeDisabled();
    await expect(page.locator(".motion-label")).toHaveText("静态流程");
    await expect(page.locator(".stage:nth-child(4)")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
      "data-running",
      "false",
    );
  } else {
    await expect(page.locator(".motion-control")).toBeHidden();
  }
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(page.viewportSize()!.width);
});

test("context loss permanently returns the flow to its local fallback", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await page.waitForFunction(() => {
    const renderer =
      document.querySelector<HTMLElement>("[data-hero-flow]")?.dataset.renderer;
    return renderer === "webgl" || renderer === "fallback";
  });
  if (
    (await page.locator("[data-hero-flow]").getAttribute("data-renderer")) !==
    "webgl"
  ) {
    testInfo.skip(true, "WebGL is unavailable in this browser");
  }
  await page.locator(".scene-canvas").dispatchEvent("webglcontextlost");
  await expect(page.locator("[data-hero-flow]")).toHaveAttribute(
    "data-renderer",
    "fallback",
  );
  await expect(page.locator(".scene-fallback")).toBeVisible();
  await expect(page.locator(".motion-control")).toBeHidden();
  await expect(page.locator(".scene-status")).toHaveText(
    "Merge and deployment remain human-controlled",
  );
});

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
