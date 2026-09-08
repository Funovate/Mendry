import { access, readFile } from "node:fs/promises";
import {
  collectFiles,
  expectedRoutes,
  localLinks,
  readHtml,
  routeToFile,
} from "./site-contract.mjs";

const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";

for (const route of expectedRoutes) {
  await access(routeToFile(route));
  const html = await readHtml(route);
  const robots = publicRelease ? "index, follow" : "noindex, nofollow";
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
  if (!html.includes('src="/media/hero-flow/scene-fallback.png"')) {
    throw new Error(`${route} is missing the hero flow fallback image`);
  }
  if (html.includes('src="/media/remediation-review.webp"')) {
    throw new Error(`${route} still renders the retired hero screenshot`);
  }
}

await access("dist/media/remediation-review.webp");
await access("dist/media/hero-flow/scene.mjs");
await access("dist/media/hero-flow/scene-fallback.png");
await access("dist/media/hero-flow/vendor/three.module.min.js");
await access("dist/media/hero-flow/vendor/three.core.min.js");
await access("dist/media/hero-flow/vendor/LICENSE");
const heroFlowScene = await readFile("dist/media/hero-flow/scene.mjs", "utf8");
if (!heroFlowScene.includes('"./vendor/three.module.min.js"')) {
  throw new Error(
    "Hero flow scene must use its relative vendored Three.js import",
  );
}
if (/https?:\/\//.test(heroFlowScene)) {
  throw new Error("Hero flow scene must not make external runtime requests");
}
await access("dist/_headers");
await access("dist/_redirects");

const robotsTxt = await readFile("dist/robots.txt", "utf8");
if (!publicRelease && !robotsTxt.includes("Disallow: /")) {
  throw new Error("Preview robots.txt must disallow all crawlers");
}

console.log(
  `Build contract: ${expectedRoutes.length} routes, ${generatedPages.length} generated pages, and static assets verified.`,
);
