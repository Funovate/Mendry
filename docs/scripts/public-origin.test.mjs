import assert from "node:assert/strict";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import os from "node:os";
import path from "node:path";
import { validatePublicOrigin } from "./public-origin.mjs";
import { availablePort } from "./verify.mjs";

const docsDirectory = path.resolve(
  fileURLToPath(new URL("..", import.meta.url)),
);
const configUrl = pathToFileURL(path.join(docsDirectory, "astro.config.mjs"));

function loadPlaywrightConfig(port) {
  const env = { ...process.env };
  if (port === undefined) delete env.PLAYWRIGHT_PORT;
  else env.PLAYWRIGHT_PORT = String(port);

  return spawnSync(
    process.execPath,
    [
      path.join(docsDirectory, "node_modules/@playwright/test/cli.js"),
      "test",
      "--list",
      "--project=desktop",
      "--config",
      path.join(docsDirectory, "playwright.config.ts"),
    ],
    { cwd: docsDirectory, env, encoding: "utf8" },
  );
}

test("Playwright config accepts the default, boundary, and dynamic ports", async () => {
  const dynamicPort = await availablePort();

  for (const port of [undefined, "1", "65535", dynamicPort]) {
    const result = loadPlaywrightConfig(port);
    assert.equal(
      result.status,
      0,
      `expected ${String(port)} to load: ${result.stdout}\n${result.stderr}`,
    );
  }
});

test("Playwright config rejects non-canonical or unsafe ports", () => {
  const invalidPorts = [
    "",
    " 4321",
    "4321 ",
    "+4321",
    "-1",
    "04321",
    "4321.0",
    "x4321",
    "4321x",
    "4321; echo injected",
    "0",
    "65536",
    "999999999999999999999999",
  ];

  for (const port of invalidPorts) {
    const result = loadPlaywrightConfig(port);
    assert.notEqual(
      result.status,
      0,
      `expected ${JSON.stringify(port)} to be rejected`,
    );
    assert.match(
      `${result.stdout}\n${result.stderr}`,
      /PLAYWRIGHT_PORT must be a canonical decimal integer between 1 and 65535/,
      `expected a clear validation error for ${JSON.stringify(port)}`,
    );
  }
});

test("the reserved custom origin is accepted", () => {
  assert.equal(
    validatePublicOrigin("https://docs.fixthe.invalid"),
    "https://docs.fixthe.invalid",
  );
  assert.equal(
    validatePublicOrigin("https://docs.fixthe.invalid/"),
    "https://docs.fixthe.invalid",
  );
});

test("invalid public origins are rejected by the shared policy", () => {
  const invalidOrigins = [
    undefined,
    "",
    "not a URL",
    "http://docs.fixthe.invalid",
    "https://pages.dev",
    "https://pages.dev.",
    "https://project.pages.dev",
    "https://project.pages.dev.",
    "https://docs.fixthe.invalid.",
    "https://docs.fixthe.invalid/docs",
    "https://docs.fixthe.invalid?release=true",
    "https://docs.fixthe.invalid#release",
    "https://user:password@docs.fixthe.invalid",
    "https://@docs.fixthe.invalid",
    "https://docs.fixthe.invalid:443",
    "https://docs.fixthe.invalid:8443",
  ];

  for (const origin of invalidOrigins) {
    assert.throws(
      () => validatePublicOrigin(origin),
      /PUBLIC_SITE_ORIGIN/,
      `expected ${String(origin)} to be rejected`,
    );
  }
});

test("invalid public origins fail before Astro writes output", () => {
  const outputDirectory = mkdtempSync(
    path.join(os.tmpdir(), "fixthe-docs-public-origin-"),
  );
  const env = {
    ...process.env,
    DOCS_PUBLIC_RELEASE: "true",
    PUBLIC_SITE_ORIGIN: "http://docs.fixthe.invalid",
  };

  try {
    const result = spawnSync(
      process.execPath,
      ["scripts/run-astro.mjs", "build", "--outDir", outputDirectory],
      { cwd: docsDirectory, env, encoding: "utf8" },
    );

    assert.notEqual(result.status, 0);
    assert.deepEqual(readdirSync(outputDirectory), []);
    assert.match(
      `${result.stdout}\n${result.stderr}`,
      /PUBLIC_SITE_ORIGIN must use HTTPS/,
    );
  } finally {
    rmSync(outputDirectory, { recursive: true, force: true });
  }
});

test("Astro config rejects a missing public origin before loading a build", () => {
  const env = { ...process.env, DOCS_PUBLIC_RELEASE: "true" };
  delete env.PUBLIC_SITE_ORIGIN;
  const result = spawnSync(
    process.execPath,
    ["--input-type=module", "-e", `import(${JSON.stringify(configUrl.href)})`],
    { cwd: docsDirectory, env, encoding: "utf8" },
  );

  assert.notEqual(result.status, 0);
  assert.match(
    `${result.stdout}\n${result.stderr}`,
    /PUBLIC_SITE_ORIGIN is required in public mode/,
  );
});
