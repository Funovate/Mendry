import assert from "node:assert/strict";
import test from "node:test";
import {
  counterpart,
  expectedRoutes,
  indexableRoutes,
  localLinks,
  nonIndexableRoutes,
  routeEntries,
  routeToFile,
} from "./site-contract.mjs";

test("every route has a reciprocal locale counterpart", () => {
  for (const route of expectedRoutes)
    assert.ok(expectedRoutes.includes(counterpart(route)));
});

test("the expected route count includes introductions and operator pages", () => {
  assert.equal(routeEntries.length, 44);
  assert.equal(expectedRoutes.length, 46);
});

test("operator entries have matching locale metadata", () => {
  assert.equal(routeEntries.length, 44);
  for (const entry of routeEntries) {
    const pair = routeEntries.find(
      (candidate) => candidate.route === counterpart(entry.route),
    );
    assert.equal(pair?.translationKey, entry.translationKey);
    assert.equal(pair?.availability, entry.availability);
    assert.notEqual(pair?.locale, entry.locale);
  }
});

test("installation remains a planned release-gate page", () => {
  const installEntries = routeEntries.filter(
    ({ translationKey }) => translationKey === "install",
  );
  assert.equal(installEntries.length, 2);
  assert.ok(
    installEntries.every(({ availability }) => availability === "planned"),
  );
});

test("local Harness walkthrough remains non-executable until validated", () => {
  const localEntries = routeEntries.filter(
    ({ translationKey }) => translationKey === "local-harness",
  );
  assert.equal(localEntries.length, 2);
  assert.ok(
    localEntries.every(({ availability }) => availability === "planned"),
  );
});

test("planned pages are excluded from the indexable route set", () => {
  assert.deepEqual(nonIndexableRoutes, [
    "/docs/get-started/local-harness/",
    "/zh-cn/docs/get-started/local-harness/",
    "/docs/get-started/install/",
    "/zh-cn/docs/get-started/install/",
  ]);
  assert.equal(indexableRoutes.length, expectedRoutes.length - 4);
  for (const route of nonIndexableRoutes)
    assert.ok(!indexableRoutes.includes(route));
});

test("routes map to static index files", () => {
  assert.equal(routeToFile("/"), "dist/index.html");
  assert.equal(routeToFile("/zh-cn/docs/"), "dist/zh-cn/docs/index.html");
});

test("localLinks keeps internal content routes only", () => {
  const html =
    '<a href="/docs/?from=intro#start">Docs</a><a href="/asset.css">CSS</a><a href="#local">Local</a><a href="https://example.com">External</a>';
  assert.deepEqual(localLinks(html), ["/docs/"]);
});
