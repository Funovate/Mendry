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
          label: "Documentation preview",
          translations: { "zh-cn": "文档预览" },
          items: [{ autogenerate: { directory: "docs" } }],
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
        {
          tag: "link",
          attrs: { rel: "preconnect", href: "https://fonts.googleapis.com" },
        },
        {
          tag: "link",
          attrs: {
            rel: "preconnect",
            href: "https://fonts.gstatic.com",
            crossorigin: true,
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "stylesheet",
            href: "https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght@9..144,300..700&display=swap",
          },
        },
      ],
      lastUpdated: true,
      pagefind: true,
      credits: true,
      disable404Route: true,
    }),
  ],
});
