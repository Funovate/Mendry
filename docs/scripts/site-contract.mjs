import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const pages = [
  [
    "agent-harness",
    "src/content/docs/docs/concepts/agent-harness.mdx",
    "preview",
  ],
  ["docs", "src/content/docs/docs/index.mdx", "available"],
  ["features", "src/content/docs/docs/reference/features.mdx", "preview"],
  [
    "operator-workflow",
    "src/content/docs/docs/guides/operator-workflow.mdx",
    "preview",
  ],
  [
    "automatic-hotfix",
    "src/content/docs/docs/guides/automatic-hotfix.mdx",
    "preview",
  ],
  ["get-started", "src/content/docs/docs/get-started.mdx", "preview"],
  [
    "local-harness",
    "src/content/docs/docs/get-started/local-harness.mdx",
    "preview",
  ],
  [
    "prerequisites",
    "src/content/docs/docs/get-started/prerequisites.mdx",
    "preview",
  ],
  ["install", "src/content/docs/docs/get-started/install.mdx", "planned"],
  ["bootstrap", "src/content/docs/docs/get-started/bootstrap.mdx", "preview"],
  [
    "first-project",
    "src/content/docs/docs/get-started/first-project.mdx",
    "preview",
  ],
  [
    "first-incident",
    "src/content/docs/docs/get-started/first-incident.mdx",
    "preview",
  ],
  [
    "architecture",
    "src/content/docs/docs/concepts/architecture.mdx",
    "available",
  ],
  ["data-model", "src/content/docs/docs/concepts/data-model.mdx", "available"],
  ["lifecycle", "src/content/docs/docs/concepts/lifecycle.mdx", "available"],
  ["security", "src/content/docs/docs/concepts/security.mdx", "available"],
  [
    "extending-harness",
    "src/content/docs/docs/guides/extending-harness.mdx",
    "preview",
  ],
  ["tencent-cls", "src/content/docs/docs/guides/tencent-cls.mdx", "preview"],
  [
    "signed-webhooks",
    "src/content/docs/docs/guides/signed-webhooks.mdx",
    "available",
  ],
  ["git-baseline", "src/content/docs/docs/guides/git-baseline.mdx", "preview"],
  [
    "llm-providers",
    "src/content/docs/docs/guides/llm-providers.mdx",
    "preview",
  ],
  [
    "troubleshooting",
    "src/content/docs/docs/guides/troubleshooting.mdx",
    "available",
  ],
  [
    "configuration",
    "src/content/docs/docs/reference/configuration.mdx",
    "available",
  ],
  ["roles", "src/content/docs/docs/reference/roles.mdx", "available"],
  ["status", "src/content/docs/docs/project/status.mdx", "available"],
];

function sourceToRoute(source) {
  const relative = source
    .replace("src/content/docs/docs/", "")
    .replace(/index\.mdx$/, "")
    .replace(/\.mdx$/, "/");
  return `/docs/${relative}`.replace(/\/+/g, "/");
}

export const routeEntries = pages.flatMap(
  ([translationKey, source, availability]) => {
    const route = sourceToRoute(source);
    return [
      { translationKey, route, source, availability, locale: "en" },
      {
        translationKey,
        route: `/zh-cn${route}`,
        source: source.replace(
          "src/content/docs/docs/",
          "src/content/docs/zh-cn/docs/",
        ),
        availability,
        locale: "zh-CN",
      },
    ];
  },
);

export const expectedRoutes = [
  "/",
  "/zh-cn/",
  ...routeEntries.map(({ route }) => route),
];

export const nonIndexableRoutes = routeEntries
  .filter(({ availability }) => availability === "planned")
  .map(({ route }) => route);

export const indexableRoutes = expectedRoutes.filter(
  (route) => !nonIndexableRoutes.includes(route),
);

export function routeToFile(route, outputRoot = "dist") {
  return path.join(outputRoot, route.replace(/^\//, ""), "index.html");
}

export function counterpart(route) {
  return route.startsWith("/zh-cn/")
    ? route.replace("/zh-cn", "")
    : route === "/zh-cn/"
      ? "/"
      : `/zh-cn${route}`;
}

export async function collectFiles(directory) {
  const entries = await readdir(directory);
  const files = [];
  for (const entry of entries) {
    const target = path.join(directory, entry);
    if ((await stat(target)).isDirectory())
      files.push(...(await collectFiles(target)));
    else files.push(target);
  }
  return files;
}

export async function readHtml(route, outputRoot = "dist") {
  return readFile(routeToFile(route, outputRoot), "utf8");
}

export function localLinks(html) {
  return [...html.matchAll(/href="(\/[^\"]*)"/g)]
    .map((match) => match[1].split(/[?#]/)[0])
    .filter((href) => !href.startsWith("//") && !href.includes("."));
}
