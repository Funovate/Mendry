import {
  packageEntryPoint,
  runNode,
  assertSuccessful,
} from "./run-command.mjs";

const [command, ...args] = process.argv.slice(2);
if (!command) {
  console.error("Usage: node scripts/run-astro.mjs <command> [...args]");
  process.exitCode = 1;
} else {
  const env = { ...process.env, ASTRO_TELEMETRY_DISABLED: "1" };
  if (command === "preview") env.ASTRO_PREVIEW_BACKGROUND = "0";

  const result = runNode(packageEntryPoint("astro"), [command, ...args], {
    env,
  });

  try {
    assertSuccessful("Astro", result);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
