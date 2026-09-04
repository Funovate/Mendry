import { describe, expect, it } from "vitest";
import { parseUnifiedDiff } from "../src/features/incidents/remediationDiff";

describe("parseUnifiedDiff", () => {
  it("pairs removed and added lines and tracks hunk line numbers", () => {
    const result = parseUnifiedDiff([
      "diff --git a/src/app.ts b/src/app.ts",
      "--- a/src/app.ts",
      "+++ b/src/app.ts",
      "@@ -1,4 +1,4 @@",
      " const ready = true;",
      "-const port = 3000;",
      "+const port = 5173;",
      " export default ready;",
    ].join("\n"));

    expect(result.parseable).toBe(true);
    expect(result.files).toHaveLength(1);
    expect(result.files[0].path).toBe("src/app.ts");
    expect(result.files[0].rows).toEqual([
      {
        original: { kind: "hunk", text: "@@ -1,4 +1,4 @@", lineNumber: null },
        changed: { kind: "hunk", text: "@@ -1,4 +1,4 @@", lineNumber: null },
      },
      {
        original: { kind: "context", text: "const ready = true;", lineNumber: 1 },
        changed: { kind: "context", text: "const ready = true;", lineNumber: 1 },
      },
      {
        original: { kind: "removed", text: "const port = 3000;", lineNumber: 2 },
        changed: { kind: "added", text: "const port = 5173;", lineNumber: 2 },
      },
      {
        original: { kind: "context", text: "export default ready;", lineNumber: 3 },
        changed: { kind: "context", text: "export default ready;", lineNumber: 3 },
      },
    ]);
  });

  it("keeps unmatched changes on their originating side", () => {
    const result = parseUnifiedDiff([
      "--- a/old.txt",
      "+++ b/new.txt",
      "@@ -1,2 +1,3 @@",
      "-old line",
      " context",
      "+new line",
      "+another line",
    ].join("\n"));

    expect(result.files[0].rows.slice(1)).toEqual([
      {
        original: { kind: "removed", text: "old line", lineNumber: 1 },
        changed: null,
      },
      {
        original: { kind: "context", text: "context", lineNumber: 2 },
        changed: { kind: "context", text: "context", lineNumber: 1 },
      },
      {
        original: null,
        changed: { kind: "added", text: "new line", lineNumber: 2 },
      },
      {
        original: null,
        changed: { kind: "added", text: "another line", lineNumber: 3 },
      },
    ]);
  });

  it("groups multiple files and falls back for non-unified text", () => {
    const result = parseUnifiedDiff([
      "diff --git a/one.txt b/one.txt",
      "--- a/one.txt",
      "+++ b/one.txt",
      "@@ -1 +1 @@",
      "-one",
      "+ONE",
      "diff --git a/two.txt b/two.txt",
      "--- a/two.txt",
      "+++ b/two.txt",
      "@@ -1 +1 @@",
      " two",
    ].join("\n"));

    expect(result.files.map((file) => file.path)).toEqual(["one.txt", "two.txt"]);
    expect(parseUnifiedDiff("not a patch")).toEqual({ files: [], parseable: false });
  });
});
