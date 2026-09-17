import assert from "node:assert/strict";
import test from "node:test";
import { verify } from "./verify.mjs";

function recordingRunner(calls, failureFor = () => undefined) {
  return (label, args, env) => {
    calls.push({ label, args, env });
    const failure = failureFor(label, args, env);
    if (failure) throw failure;
  };
}

test("verify restores and asserts preview output after a public build failure", async () => {
  const calls = [];
  const publicFailure = new Error("public contract failed");

  await assert.rejects(
    verify({
      getPort: async () => 4310,
      runCommand: recordingRunner(calls, (label) =>
        label === "public static build contract" ? publicFailure : undefined,
      ),
    }),
    (error) => error === publicFailure,
  );

  assert.deepEqual(
    calls.slice(-2).map(({ label }) => label),
    ["final preview static build (restore)", "final preview output contract"],
  );
  for (const { label, env } of calls.slice(-2)) {
    assert.equal(
      env.DOCS_PUBLIC_RELEASE,
      undefined,
      `${label} must be preview mode`,
    );
    assert.equal(
      env.PUBLIC_SITE_ORIGIN,
      undefined,
      `${label} must be preview mode`,
    );
  }
});

test("verify preserves both the original and restoration failures", async () => {
  const calls = [];
  const publicFailure = new Error("public contract failed");
  const restoreFailure = new Error("preview restore failed");

  await assert.rejects(
    verify({
      getPort: async () => 4311,
      runCommand: recordingRunner(calls, (label) => {
        if (label === "public static build contract") return publicFailure;
        if (label === "final preview static build (restore)")
          return restoreFailure;
        return undefined;
      }),
    }),
    (error) =>
      error instanceof AggregateError &&
      error.errors.includes(publicFailure) &&
      error.errors.includes(restoreFailure),
  );

  assert.equal(calls.at(-1).label, "final preview output contract");
});
