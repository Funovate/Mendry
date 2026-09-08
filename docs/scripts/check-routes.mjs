import { access } from "node:fs/promises";
import { counterpart, expectedRoutes } from "./site-contract.mjs";

const routeSources = new Map([
  ["/", "src/pages/index.astro"],
  ["/docs/", "src/content/docs/docs/index.mdx"],
  ["/docs/get-started/", "src/content/docs/docs/get-started.mdx"],
  [
    "/docs/concepts/architecture/",
    "src/content/docs/docs/concepts/architecture.mdx",
  ],
  ["/zh-cn/", "src/pages/zh-cn/index.astro"],
  ["/zh-cn/docs/", "src/content/docs/zh-cn/docs/index.mdx"],
  ["/zh-cn/docs/get-started/", "src/content/docs/zh-cn/docs/get-started.mdx"],
  [
    "/zh-cn/docs/concepts/architecture/",
    "src/content/docs/zh-cn/docs/concepts/architecture.mdx",
  ],
]);

for (const route of expectedRoutes) {
  await access(routeSources.get(route));
  if (!expectedRoutes.includes(counterpart(route))) {
    throw new Error(`Missing locale counterpart for ${route}`);
  }
}

console.log(
  `Route parity: ${expectedRoutes.length} localized routes verified.`,
);
