import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

export const expectedRoutes = [
  "/",
  "/docs/",
  "/docs/get-started/",
  "/docs/concepts/architecture/",
  "/zh-cn/",
  "/zh-cn/docs/",
  "/zh-cn/docs/get-started/",
  "/zh-cn/docs/concepts/architecture/",
];

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
  return [...html.matchAll(/href="(\/[^"]*)"/g)]
    .map((match) => match[1].split(/[?#]/)[0])
    .filter((href) => !href.startsWith("//") && !href.includes("."));
}
