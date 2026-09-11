import { readFile } from "node:fs/promises";
import path from "node:path";
import {
  collectFiles,
  counterpart,
  expectedRoutes,
  indexableRoutes,
  nonIndexableRoutes,
  readHtml,
} from "./site-contract.mjs";
import { validatePublicOrigin } from "./public-origin.mjs";

const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";
const configuredOrigin = process.env.PUBLIC_SITE_ORIGIN;

function fail(message) {
  throw new Error(`Release build contract: ${message}`);
}

function attribute(tag, name) {
  return tag.match(new RegExp(`(?:^|\\s)${name}="([^"]*)"`))?.[1];
}

function tags(html, tagName) {
  return html.match(new RegExp(`<${tagName}\\b[^>]*>`, "g")) ?? [];
}

function findMeta(html, name, value) {
  return tags(html, "meta").find(
    (tag) =>
      attribute(tag, "name") === name &&
      (value === undefined || attribute(tag, "content") === value),
  );
}

function findPropertyMeta(html, property) {
  return tags(html, "meta").find(
    (tag) => attribute(tag, "property") === property,
  );
}

function findLink(html, rel, predicate = () => true) {
  return tags(html, "link").find(
    (tag) => attribute(tag, "rel") === rel && predicate(tag),
  );
}

function requireMatch(condition, message) {
  if (!condition) fail(message);
}

function publicOrigin() {
  try {
    return validatePublicOrigin(configuredOrigin);
  } catch (error) {
    fail(error.message);
  }
}

const origin = publicRelease ? publicOrigin() : null;
const routes = expectedRoutes;

for (const route of routes) {
  const html = await readHtml(route);
  const indexable = !nonIndexableRoutes.includes(route);
  const robots = publicRelease
    ? indexable
      ? "index, follow"
      : "noindex, follow"
    : "noindex, nofollow";
  requireMatch(
    findMeta(html, "robots", robots),
    `${route} is missing robots metadata: ${robots}`,
  );

  const canonical = findLink(html, "canonical");
  const alternates = tags(html, "link").filter(
    (tag) => attribute(tag, "rel") === "alternate",
  );

  if (publicRelease) {
    requireMatch(
      canonical && attribute(canonical, "href") === `${origin}${route}`,
      `${route} must emit its canonical URL at ${origin}${route}`,
    );

    const englishRoute = route.startsWith("/zh-cn/")
      ? counterpart(route)
      : route;
    const chineseRoute = route.startsWith("/zh-cn/") ? route : `/zh-cn${route}`;
    const alternateExpectations = [
      ["en", `${origin}${englishRoute}`],
      ["zh-CN", `${origin}${chineseRoute}`],
      ["x-default", `${origin}${englishRoute}`],
    ];

    for (const [language, href] of alternateExpectations) {
      requireMatch(
        alternates.some(
          (tag) =>
            attribute(tag, "hreflang") === language &&
            attribute(tag, "href") === href,
        ),
        `${route} must link to ${language} at ${href}`,
      );
    }
  } else {
    requireMatch(
      !canonical,
      `${route} emitted a canonical URL in preview mode`,
    );
    requireMatch(
      alternates.length === 0,
      `${route} emitted locale alternates in preview mode`,
    );
    requireMatch(
      !findPropertyMeta(html, "og:image"),
      `${route} emitted an Open Graph image in preview mode`,
    );
  }

  if (publicRelease) {
    const image = findPropertyMeta(html, "og:image");
    requireMatch(image, `${route} is missing its Open Graph image`);
    let imageUrl;
    try {
      imageUrl = new URL(attribute(image, "content"));
    } catch {
      fail(`${route} has a non-absolute Open Graph image URL`);
    }
    requireMatch(
      imageUrl.origin === origin,
      `${route} has an Open Graph image outside ${origin}`,
    );
    for (const name of [
      "twitter:title",
      "twitter:description",
      "twitter:image",
    ]) {
      requireMatch(
        findMeta(html, name),
        `${route} is missing ${name} metadata`,
      );
    }
    requireMatch(
      html.includes('type="application/ld+json"') === indexable,
      `${route} must ${indexable ? "emit" : "omit"} structured data`,
    );
  }
}

const files = await collectFiles("dist");
const sitemapFiles = files.filter((file) => {
  const name = path.basename(file);
  return name === "sitemap-index.xml" || /^sitemap-\d+\.xml$/.test(name);
});
const robotsTxt = await readFile("dist/robots.txt", "utf8");

if (publicRelease) {
  const sitemapUrl = `${origin}/sitemap-index.xml`;
  requireMatch(
    robotsTxt.includes("User-agent: *\nAllow: /\n") &&
      robotsTxt.includes(`Sitemap: ${sitemapUrl}`) &&
      !robotsTxt.includes("Disallow: /"),
    "public robots.txt must allow crawling and reference the sitemap index",
  );
  requireMatch(
    sitemapFiles.some((file) => path.basename(file) === "sitemap-index.xml"),
    "public build must emit sitemap-index.xml",
  );
  requireMatch(
    sitemapFiles.some((file) => path.basename(file) === "sitemap-0.xml"),
    "public build must emit sitemap-0.xml",
  );

  const sitemapIndex = await readFile("dist/sitemap-index.xml", "utf8");
  const sitemapReferences = [
    ...sitemapIndex.matchAll(/<loc>([^<]+)<\/loc>/g),
  ].map((match) => match[1]);
  requireMatch(
    sitemapReferences.length > 0,
    "sitemap index must contain a child sitemap",
  );
  for (const sitemapReference of sitemapReferences) {
    requireMatch(
      sitemapReference.startsWith(`${origin}/`),
      `sitemap index contains an external URL: ${sitemapReference}`,
    );
    requireMatch(
      sitemapFiles.some(
        (file) =>
          path.basename(file) ===
          path.basename(new URL(sitemapReference).pathname),
      ),
      `sitemap index references a missing file: ${sitemapReference}`,
    );
  }

  const sitemap = await readFile("dist/sitemap-0.xml", "utf8");
  for (const route of indexableRoutes) {
    requireMatch(
      sitemap.includes(`<loc>${origin}${route}</loc>`),
      `sitemap is missing ${origin}${route}`,
    );
  }
  for (const route of nonIndexableRoutes) {
    requireMatch(
      !sitemap.includes(`<loc>${origin}${route}</loc>`),
      `sitemap must exclude non-indexable route ${origin}${route}`,
    );
  }
} else {
  requireMatch(
    robotsTxt.includes("User-agent: *\nDisallow: /"),
    "preview robots.txt must disallow all crawlers",
  );
  requireMatch(
    !robotsTxt.includes("Sitemap:"),
    "preview robots.txt must not advertise a sitemap",
  );
  requireMatch(
    sitemapFiles.length === 0,
    "preview build must not emit sitemap files",
  );
}

console.log(
  `Release build contract: ${publicRelease ? "public" : "preview"} metadata, locale links, robots, and sitemap output verified.`,
);
