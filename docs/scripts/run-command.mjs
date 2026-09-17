import { existsSync, readFileSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";

const require = createRequire(import.meta.url);

function existingPath(value) {
  if (!value || value.toLowerCase().endsWith(".cmd")) return null;
  const candidate = isAbsolute(value) ? value : resolve(process.cwd(), value);
  return existsSync(candidate) ? candidate : null;
}

export function resolveNpmCli() {
  const configured = existingPath(process.env.npm_execpath);
  if (configured) return configured;

  const nodeBin = dirname(process.execPath);
  const bundledNpmCandidates = [
    resolve(nodeBin, "node_modules", "npm", "bin", "npm-cli.js"),
    resolve(nodeBin, "..", "node_modules", "npm", "bin", "npm-cli.js"),
    resolve(nodeBin, "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
  ];
  const bundledNpm = bundledNpmCandidates.find((candidate) =>
    existsSync(candidate),
  );
  if (bundledNpm) return bundledNpm;

  throw new Error(
    "Could not locate npm-cli.js; run this command with a Node.js installation that includes npm",
  );
}

export function runNode(entrypoint, args, options = {}) {
  return spawnSync(process.execPath, [entrypoint, ...args], {
    ...options,
    stdio: options.stdio ?? "inherit",
  });
}

export function runNpm(args, options = {}) {
  return runNode(resolveNpmCli(), args, options);
}

export function assertSuccessful(label, result) {
  if (result.error) {
    throw new Error(`${label} could not start: ${result.error.message}`, {
      cause: result.error,
    });
  }
  if (result.status !== 0) {
    const status =
      result.status === null
        ? `signal ${result.signal}`
        : `exit ${result.status}`;
    throw new Error(`${label} failed (${status})`);
  }
}

export function runNpmScript(label, args, options = {}) {
  const result = runNpm(args, options);
  assertSuccessful(label, result);
  return result;
}

export function packageEntryPoint(packageName, binName = packageName) {
  const packageJson = require.resolve(`${packageName}/package.json`, {
    paths: [process.cwd()],
  });
  const packageDirectory = dirname(packageJson);
  const packageManifest = JSON.parse(readFileSync(packageJson, "utf8"));
  const bin =
    typeof packageManifest.bin === "string"
      ? packageManifest.bin
      : packageManifest.bin?.[binName];
  if (!bin) throw new Error(`${packageName} does not expose a ${binName} CLI`);
  return join(packageDirectory, bin);
}
