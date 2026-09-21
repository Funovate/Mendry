import { defineConfig } from "astro/config";
import sitemap from "@astrojs/sitemap";
import starlight from "@astrojs/starlight";
import { validatePublicOrigin } from "./scripts/public-origin.mjs";
import { nonIndexableRoutes } from "./scripts/site-contract.mjs";

const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";
const configuredOrigin = process.env.PUBLIC_SITE_ORIGIN;
const publicOrigin = publicRelease
  ? validatePublicOrigin(configuredOrigin)
  : undefined;

export default defineConfig({
  site: publicOrigin,
  output: "static",
  integrations: [
    sitemap({
      filter: (page) => !nonIndexableRoutes.includes(new URL(page).pathname),
      i18n: {
        defaultLocale: "root",
        locales: { root: "en", "zh-cn": "zh-CN" },
      },
    }),
    starlight({
      title: "Mendry",
      logo: {
        light: "./public/brand/mendry-logo-horizontal.svg",
        dark: "./public/brand/mendry-logo-horizontal-reversed.svg",
        replacesTitle: true,
      },
      favicon: "/brand/mendry-icon-tile.svg",
      description:
        "Self-hosted Agent Harness for bounded model and tool execution, traceable artifacts, and task-defined completion.",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/Funovate/Mendry",
        },
      ],
      defaultLocale: "root",
      locales: {
        root: { label: "English", lang: "en" },
        "zh-cn": { label: "简体中文", lang: "zh-CN" },
      },
      sidebar: [
        {
          label: "Overview",
          translations: { "zh-CN": "概览" },
          items: [
            {
              slug: "docs",
              label: "Documentation",
              translations: { "zh-CN": "文档概览" },
            },
          ],
        },
        {
          label: "Agent Harness",
          translations: { "zh-CN": "Agent Harness" },
          items: [
            {
              slug: "docs/concepts/agent-harness",
              label: "Core contracts",
              translations: { "zh-CN": "核心契约" },
            },
            {
              slug: "docs/get-started/local-harness",
              label: "Run Local Harness",
              translations: { "zh-CN": "运行本地 Harness" },
            },
            {
              slug: "docs/guides/extending-harness",
              label: "Extend the Harness",
              translations: { "zh-CN": "扩展 Harness" },
            },
          ],
        },
        {
          label: "Get started",
          translations: { "zh-CN": "入门" },
          items: [
            {
              slug: "docs/get-started",
              label: "Choose a path",
              translations: { "zh-CN": "选择路径" },
            },
            {
              slug: "docs/get-started/prerequisites",
              label: "Incident app prerequisites",
              translations: { "zh-CN": "事故应用前提" },
            },
            {
              slug: "docs/get-started/install",
              label: "Incident app installation",
              translations: { "zh-CN": "事故应用安装" },
            },
            {
              slug: "docs/get-started/bootstrap",
              label: "Bootstrap login",
              translations: { "zh-CN": "初始化登录账号" },
            },
            {
              slug: "docs/get-started/first-project",
              label: "First project",
              translations: { "zh-CN": "首个项目" },
            },
            {
              slug: "docs/get-started/first-incident",
              label: "First incident",
              translations: { "zh-CN": "首个事故" },
            },
          ],
        },
        {
          label: "Concepts",
          translations: { "zh-CN": "核心概念" },
          items: [
            {
              slug: "docs/concepts/architecture",
              label: "Architecture",
              translations: { "zh-CN": "架构" },
            },
            {
              slug: "docs/concepts/data-model",
              label: "Data model",
              translations: { "zh-CN": "数据模型" },
            },
            {
              slug: "docs/concepts/lifecycle",
              label: "Lifecycle",
              translations: { "zh-CN": "生命周期" },
            },
            {
              slug: "docs/concepts/security",
              label: "Security boundaries",
              translations: { "zh-CN": "安全边界" },
            },
          ],
        },
        {
          label: "Guides",
          translations: { "zh-CN": "指南" },
          items: [
            {
              slug: "docs/guides/operator-workflow",
              label: "Operator workflow",
              translations: { "zh-CN": "日常操作流程" },
            },
            {
              slug: "docs/guides/automatic-hotfix",
              label: "Automatic hotfix",
              translations: { "zh-CN": "自动修复与预验证" },
            },
            {
              slug: "docs/guides/tencent-cls",
              label: "Tencent CLS",
              translations: { "zh-CN": "腾讯云 CLS" },
            },
            {
              slug: "docs/guides/managed-log-probe",
              label: "SSH log monitoring",
              translations: { "zh-CN": "SSH 日志监控" },
            },
            {
              slug: "docs/guides/signed-webhooks",
              label: "Signed webhooks",
              translations: { "zh-CN": "签名 Webhook" },
            },
            {
              slug: "docs/guides/notifications",
              label: "Project notifications",
              translations: { "zh-CN": "项目通知" },
            },
            {
              slug: "docs/guides/git-baseline",
              label: "Git and baseline",
              translations: { "zh-CN": "Git 与基线" },
            },
            {
              slug: "docs/guides/llm-providers",
              label: "LLM providers",
              translations: { "zh-CN": "LLM 提供方" },
            },
            {
              slug: "docs/guides/troubleshooting",
              label: "Troubleshooting",
              translations: { "zh-CN": "故障排查" },
            },
          ],
        },
        {
          label: "Reference",
          translations: { "zh-CN": "参考" },
          items: [
            {
              slug: "docs/reference/features",
              label: "Feature map",
              translations: { "zh-CN": "功能导航" },
            },
            {
              slug: "docs/reference/configuration",
              label: "Configuration",
              translations: { "zh-CN": "配置" },
            },
            {
              slug: "docs/reference/roles",
              label: "Authentication",
              translations: { "zh-CN": "认证" },
            },
            {
              slug: "docs/project/status",
              label: "Product status",
              translations: { "zh-CN": "产品状态" },
            },
          ],
        },
      ],
      customCss: ["./src/styles/starlight.css"],
      components: {
        Head: "./src/components/DocumentHead.astro",
        Header: "./src/components/Header.astro",
        PageTitle: "./src/components/PageTitle.astro",
        SkipLink: "./src/components/SkipLink.astro",
      },
      head: [
        {
          tag: "link",
          attrs: {
            rel: "apple-touch-icon",
            href: "/brand/mendry-icon-tile-512.png",
          },
        },
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
