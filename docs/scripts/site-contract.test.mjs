import assert from "node:assert/strict";
import test from "node:test";
import {
  counterpart,
  expectedRoutes,
  localLinks,
  routeToFile,
} from "./site-contract.mjs";

test("every route has a reciprocal locale counterpart", () => {
  for (const route of expectedRoutes)
    assert.ok(expectedRoutes.includes(counterpart(route)));
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
