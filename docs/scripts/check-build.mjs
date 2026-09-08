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

await access("dist/media/remediation-review.webp");
await access("dist/_headers");
await access("dist/_redirects");

const robotsTxt = await readFile("dist/robots.txt", "utf8");
if (!publicRelease && !robotsTxt.includes("Disallow: /")) {
  throw new Error("Preview robots.txt must disallow all crawlers");
}

console.log(
  `Build contract: ${expectedRoutes.length} routes, ${generatedPages.length} generated pages, and static assets verified.`,
);
