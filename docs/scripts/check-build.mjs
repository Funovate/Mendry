import { access, readFile } from "node:fs/promises";
import {
  collectFiles,
  expectedRoutes,
  localLinks,
  nonIndexableRoutes,
  readHtml,
  routeToFile,
} from "./site-contract.mjs";

const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";

for (const route of expectedRoutes) {
  await access(routeToFile(route));
  const html = await readHtml(route);
  const indexable = !nonIndexableRoutes.includes(route);
  const robots = publicRelease
    ? indexable
      ? "index, follow"
      : "noindex, follow"
    : "noindex, nofollow";
  if (!html.includes(`content="${robots}"`)) {
    throw new Error(`${route} is missing robots metadata: ${robots}`);
  }
  if (!publicRelease && /rel="canonical"/.test(html)) {
    throw new Error(`${route} emitted a canonical URL in preview mode`);
  }
}

const generatedPages = (await collectFiles("dist")).filter((file) =>
  file.endsWith(".html"),
);
for (const page of generatedPages) {
  const html = await readFile(page, "utf8");
  for (const href of localLinks(html)) {
    try {
      await access(routeToFile(href));
    } catch {
      throw new Error(`${page} links to missing internal page ${href}`);
    }
  }
}

for (const route of ["/", "/zh-cn/"]) {
  const html = await readHtml(route);
  if (!html.includes("data-execution-model")) {
    throw new Error(`${route} is missing the semantic execution model`);
  }
  if (/<canvas\b/i.test(html)) {
    throw new Error(`${route} still renders a canvas-based homepage visual`);
  }
  if (html.includes("/media/hero-flow/") || html.includes("scene-fallback")) {
    throw new Error(`${route} still references the retired 3D hero scene`);
  }
  if (!html.includes("incident-sequence")) {
    throw new Error(`${route} is missing the semantic incident sequence`);
  }
}

await access("dist/media/remediation-review.webp");
await access("dist/media/execution-model.png");
await access("dist/_headers");
await access("dist/_redirects");

const robotsTxt = await readFile("dist/robots.txt", "utf8");
if (!publicRelease && !robotsTxt.includes("Disallow: /")) {
  throw new Error("Preview robots.txt must disallow all crawlers");
}

console.log(
  `Build contract: ${expectedRoutes.length} routes, ${generatedPages.length} generated pages, and static assets verified.`,
);
