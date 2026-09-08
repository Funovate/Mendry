import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

for (const locale of ["en", "zh-cn"]) {
  for (const theme of ["light", "dark"]) {
    test(`${locale} ${theme} refined introduction`, async ({
      page,
    }, testInfo) => {
      await page.goto(locale === "en" ? "/" : "/zh-cn/");
      await page.locator("starlight-theme-select select").selectOption(theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(
        page.locator(".intro-product-frame figcaption"),
      ).toContainText(locale === "en" ? "Synthetic data" : "合成演示数据");
      await expect(page.locator(".intro-workflow .intro-kicker")).toContainText(
        locale === "en" ? "Workflow" : "工作流程",
      );

      const kicker = await page
        .locator(".intro-workflow .intro-kicker")
        .boundingBox();
      expect(kicker).not.toBeNull();
      expect(kicker!.y + kicker!.height).toBeLessThan(
        page.viewportSize()!.height,
      );
      const media = await page
        .locator(".intro-product-frame img")
        .boundingBox();
      expect(media).not.toBeNull();
      expect(media!.width / media!.height).toBeCloseTo(16 / 9, 1);
      const accessibility = await new AxeBuilder({ page }).analyze();
      expect(accessibility.violations).toEqual([]);
      await testInfo.attach(`${locale}-${theme}`, {
        body: await page.screenshot({ fullPage: true }),
        contentType: "image/png",
      });

      await page.evaluate(() => {
        document.documentElement.style.zoom = "2";
      });
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(page.viewportSize()!.width);
      await expect(
        page.locator(".intro-readiness .intro-action"),
      ).toBeVisible();
      await page.evaluate(() => {
        document.documentElement.style.zoom = "";
      });
      await page.locator(".intro-action-secondary").click();
      await expect(page).toHaveURL(/\/docs\/concepts\/architecture\/$/);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    });
  }
}
