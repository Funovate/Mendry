import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { parse } from "yaml";
import { counterpart, expectedRoutes, routeEntries } from "./site-contract.mjs";

function readFrontmatter(source, file) {
  const match = source.match(/^---\n([\s\S]*?)\n---/);
  if (!match) throw new Error(`${file} is missing YAML frontmatter`);
  return parse(match[1]);
}

const entriesByRoute = new Map(
  routeEntries.map((entry) => [entry.route, entry]),
);

for (const route of expectedRoutes) {
  if (!expectedRoutes.includes(counterpart(route))) {
    throw new Error(`Missing locale counterpart for ${route}`);
  }
}

for (const entry of routeEntries) {
  await access(entry.source);
  const source = await readFile(entry.source, "utf8");
  const frontmatter = readFrontmatter(source, entry.source);

  if (frontmatter.translationKey !== entry.translationKey) {
    throw new Error(`${entry.source} has the wrong translationKey`);
  }
  if (frontmatter.availability !== entry.availability) {
    throw new Error(`${entry.source} has the wrong availability`);
  }
  const citations = frontmatter.sources;
  if (!Array.isArray(citations) || citations.length === 0) {
    throw new Error(`${entry.source} must cite at least one source`);
  }
  for (const citation of citations) {
    await access(path.resolve("..", citation));
  }
  if (
    entry.availability === "planned" &&
    /```(?:sh|bash|shell|console)\b/.test(source)
  ) {
    throw new Error(
      `${entry.source} is planned and cannot contain shell commands`,
    );
  }

  const paired = entriesByRoute.get(counterpart(entry.route));
  if (
    !paired ||
    paired.translationKey !== entry.translationKey ||
    paired.availability !== entry.availability
  ) {
    throw new Error(`${entry.route} has inconsistent locale metadata`);
  }
}

const terminology = JSON.parse(
  await readFile("src/content/terminology.json", "utf8"),
);
const termKeys = terminology.terms.map(({ key }) => key);
if (new Set(termKeys).size !== termKeys.length) {
  throw new Error("Terminology keys must be unique");
}
for (const status of ["available", "preview", "planned"]) {
  if (!terminology.literal.includes(status)) {
    throw new Error(`Terminology must keep ${status} literal`);
  }
}

console.log(
  `Route parity: ${routeEntries.length} operator routes and 2 introductions verified.`,
);
