import { runNpmScript } from "./run-command.mjs";

const env = {
  ...process.env,
  DOCS_PUBLIC_RELEASE: "true",
  PUBLIC_SITE_ORIGIN: "https://mendry.net",
};

runNpmScript("Public build", ["run", "build"], { env });
