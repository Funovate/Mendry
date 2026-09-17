import { createServer } from "node:net";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { runNpmScript } from "./run-command.mjs";

export function previewEnvironment(base = process.env) {
  const env = { ...base };
  delete env.DOCS_PUBLIC_RELEASE;
  delete env.PUBLIC_SITE_ORIGIN;
  delete env.PLAYWRIGHT_BASE_URL;
  delete env.PLAYWRIGHT_PORT;
  return env;
}

export function availablePort() {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : null;
      server.close((error) => {
        if (error) reject(error);
        else if (port) resolvePort(port);
        else reject(new Error("Could not determine an available port"));
      });
    });
  });
}

export function run(label, args, env = process.env) {
  console.log(`\n== ${label} ==`);
  return runNpmScript(label, args, { env });
}

function combineFailures(primaryFailure, restorationFailure) {
  if (!primaryFailure) return restorationFailure;
  if (!restorationFailure) return primaryFailure;
  return new AggregateError(
    [primaryFailure, restorationFailure],
    "Documentation verification failed and preview restoration failed",
  );
}

export async function verify({
  runCommand = run,
  getPort = availablePort,
} = {}) {
  const previewEnv = previewEnvironment();
  let primaryFailure;

  try {
    runCommand("formatting", ["run", "lint"]);
    runCommand("Astro and content checks", ["run", "check"]);
    runCommand("Node contract tests", ["run", "test"]);
    runCommand("production dependency audit", ["run", "audit:production"]);
    runCommand("preview static build", ["run", "build"], previewEnv);

    const browserEnv = {
      ...previewEnv,
      PLAYWRIGHT_PORT: String(await getPort()),
    };
    runCommand("Playwright browser tests", ["run", "test:e2e"], browserEnv);
    runCommand(
      "public static build contract",
      ["run", "build:public"],
      previewEnv,
    );
  } catch (error) {
    primaryFailure = error;
  }

  const restorationFailures = [];
  try {
    runCommand(
      "final preview static build (restore)",
      ["run", "build"],
      previewEnv,
    );
  } catch (error) {
    restorationFailures.push(error);
  }
  try {
    runCommand(
      "final preview output contract",
      ["run", "check:release"],
      previewEnv,
    );
  } catch (error) {
    restorationFailures.push(error);
  }

  const restorationFailure =
    restorationFailures.length === 0
      ? undefined
      : restorationFailures.length === 1
        ? restorationFailures[0]
        : new AggregateError(restorationFailures, "Preview restoration failed");
  const failure = combineFailures(primaryFailure, restorationFailure);
  if (failure) throw failure;

  console.log(
    "\nDocumentation verification completed with preview output restored.",
  );
}

const isMain =
  process.argv[1] &&
  pathToFileURL(resolve(process.argv[1])).href === import.meta.url;

if (isMain) {
  verify().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
