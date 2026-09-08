import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";
const configuredOrigin = process.env.PUBLIC_SITE_ORIGIN;

if (publicRelease && !configuredOrigin) {
  throw new Error(
    "PUBLIC_SITE_ORIGIN is required when DOCS_PUBLIC_RELEASE=true",
  );
}

if (publicRelease) {
  const origin = new URL(configuredOrigin);
  if (origin.protocol !== "https:" || origin.hostname.endsWith(".pages.dev")) {
    throw new Error(
      "PUBLIC_SITE_ORIGIN must be an approved HTTPS custom domain",
    );
  }
}

export default defineConfig({
  site: publicRelease ? configuredOrigin : undefined,
  output: "static",
  integrations: [
    starlight({
      title: "FixThe",
      description:
        "Customer-managed production incident investigation and controlled remediation.",
      defaultLocale: "root",
      locales: {
        root: { label: "English", lang: "en" },
        "zh-cn": { label: "简体中文", lang: "zh-CN" },
      },
      sidebar: [
        {
          label: "Overview",
          translations: { "zh-cn": "概览" },
          items: [
            {
              slug: "docs",
              label: "Documentation",
              translations: { "zh-cn": "文档概览" },
            },
          ],
        },
        {
          label: "Get started",
          translations: { "zh-cn": "入门" },
          items: [
            {
              slug: "docs/get-started",
              label: "Journey overview",
              translations: { "zh-cn": "流程概览" },
            },
            {
              slug: "docs/get-started/prerequisites",
              label: "Prerequisites",
              translations: { "zh-cn": "前提条件" },
            },
            {
              slug: "docs/get-started/install",
              label: "Installation status",
              translations: { "zh-cn": "安装状态" },
            },
            {
              slug: "docs/get-started/bootstrap",
              label: "Bootstrap administrator",
              translations: { "zh-cn": "初始化管理员" },
            },
            {
              slug: "docs/get-started/first-project",
              label: "First project",
              translations: { "zh-cn": "首个项目" },
            },
            {
              slug: "docs/get-started/first-incident",
              label: "First incident",
              translations: { "zh-cn": "首个事故" },
            },
          ],
        },
        {
          label: "Concepts",
          translations: { "zh-cn": "核心概念" },
          items: [
            {
              slug: "docs/concepts/architecture",
              label: "Architecture",
              translations: { "zh-cn": "架构" },
            },
            {
              slug: "docs/concepts/data-model",
              label: "Data model",
              translations: { "zh-cn": "数据模型" },
            },
            {
              slug: "docs/concepts/lifecycle",
              label: "Lifecycle",
              translations: { "zh-cn": "生命周期" },
            },
            {
              slug: "docs/concepts/security",
              label: "Security boundaries",
              translations: { "zh-cn": "安全边界" },
            },
          ],
        },
        {
          label: "Guides",
          translations: { "zh-cn": "指南" },
          items: [
            {
              slug: "docs/guides/tencent-cls",
              label: "Tencent CLS",
              translations: { "zh-cn": "腾讯云 CLS" },
            },
            {
              slug: "docs/guides/signed-webhooks",
              label: "Signed webhooks",
              translations: { "zh-cn": "签名 Webhook" },
            },
            {
              slug: "docs/guides/git-baseline",
              label: "Git and baseline",
              translations: { "zh-cn": "Git 与基线" },
            },
            {
              slug: "docs/guides/llm-providers",
              label: "LLM providers",
              translations: { "zh-cn": "LLM 提供方" },
            },
            {
              slug: "docs/guides/troubleshooting",
              label: "Troubleshooting",
              translations: { "zh-cn": "故障排查" },
            },
          ],
        },
        {
          label: "Reference",
          translations: { "zh-cn": "参考" },
          items: [
            {
              slug: "docs/reference/configuration",
              label: "Configuration",
              translations: { "zh-cn": "配置" },
            },
            {
              slug: "docs/reference/roles",
              label: "Roles",
              translations: { "zh-cn": "角色" },
            },
            {
              slug: "docs/project/status",
              label: "Product status",
              translations: { "zh-cn": "产品状态" },
            },
          ],
        },
      ],
      customCss: ["./src/styles/starlight.css"],
      components: {
        Head: "./src/components/DocumentHead.astro",
        PageTitle: "./src/components/PageTitle.astro",
        SkipLink: "./src/components/SkipLink.astro",
      },
      head: [
        {
          tag: "meta",
          attrs: {
            name: "robots",
            content: publicRelease ? "index, follow" : "noindex, nofollow",
          },
        },
        {
          tag: "meta",
          attrs: { name: "theme-color", content: "#17212b" },
        },
      ],
      lastUpdated: true,
      pagefind: true,
      credits: true,
      disable404Route: true,
    }),
  ],
});
