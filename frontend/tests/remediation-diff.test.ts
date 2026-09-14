import { afterEach, describe, expect, it } from "vitest";
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

describe("DiffViewer UI", () => {
  afterEach(async () => {
    const { cleanup } = await import("@testing-library/react");
    cleanup();
  });
  const sampleDiff = [
    "diff --git a/src/first.ts b/src/first.ts",
    "--- a/src/first.ts",
    "+++ b/src/first.ts",
    "@@ -1,2 +1,2 @@",
    "-const oldA = 1;",
    "+const newA = 2;",
    "diff --git a/src/second.ts b/src/second.ts",
    "--- a/src/second.ts",
    "+++ b/src/second.ts",
    "@@ -1,2 +1,2 @@",
    "-const oldB = 1;",
    "+const newB = 2;",
  ].join("\n");

  it("collapses modified code files by default", async () => {
    const { createElement } = await import("react");
    const { render, screen } = await import("@testing-library/react");
    const { DiffViewer } = await import("../src/features/incidents/RemediationPanel");

    render(createElement(DiffViewer, { value: sampleDiff }));

    // File headers should be rendered
    expect(screen.getByText("src/first.ts")).toBeInTheDocument();
    expect(screen.getByText("src/second.ts")).toBeInTheDocument();

    // But code panes should NOT be visible by default (default collapsed)
    expect(screen.queryByText("const oldA = 1;")).not.toBeInTheDocument();
    expect(screen.queryByText("const newA = 2;")).not.toBeInTheDocument();
    expect(screen.queryByText("const oldB = 1;")).not.toBeInTheDocument();
    expect(screen.queryByText("const newB = 2;")).not.toBeInTheDocument();

    // Toggle all button should say "Expand all"
    expect(screen.getByRole("button", { name: /expand all/i })).toBeInTheDocument();
  });

  it("expands a single file when its header is clicked and collapses when clicked again", async () => {
    const { createElement } = await import("react");
    const { fireEvent, render, screen } = await import("@testing-library/react");
    const { DiffViewer } = await import("../src/features/incidents/RemediationPanel");

    render(createElement(DiffViewer, { value: sampleDiff }));

    const firstHeader = screen.getByRole("button", { name: /src\/first\.ts/i });
    expect(firstHeader).toHaveAttribute("aria-expanded", "false");

    // Click to expand first file
    fireEvent.click(firstHeader);
    expect(firstHeader).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("const oldA = 1;")).toBeInTheDocument();
    expect(screen.getByText("const newA = 2;")).toBeInTheDocument();

    // Second file should remain collapsed
    expect(screen.queryByText("const oldB = 1;")).not.toBeInTheDocument();

    // Click again to collapse
    fireEvent.click(firstHeader);
    expect(firstHeader).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("const oldA = 1;")).not.toBeInTheDocument();
  });

  it("toggles all files when Expand all / Collapse all is clicked", async () => {
    const { createElement } = await import("react");
    const { fireEvent, render, screen } = await import("@testing-library/react");
    const { DiffViewer } = await import("../src/features/incidents/RemediationPanel");

    render(createElement(DiffViewer, { value: sampleDiff }));

    const toggleAllBtn = screen.getByRole("button", { name: /expand all/i });

    // Click Expand all
    fireEvent.click(toggleAllBtn);

    // Both files should be expanded
    expect(screen.getByText("const oldA = 1;")).toBeInTheDocument();
    expect(screen.getByText("const oldB = 1;")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /collapse all/i })).toBeInTheDocument();

    // Click Collapse all
    fireEvent.click(screen.getByRole("button", { name: /collapse all/i }));

    // Both files should now be collapsed
    expect(screen.queryByText("const oldA = 1;")).not.toBeInTheDocument();
    expect(screen.queryByText("const oldB = 1;")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /expand all/i })).toBeInTheDocument();
  });

  it("collapses fallback raw diff by default and expands on click", async () => {
    const { createElement } = await import("react");
    const { fireEvent, render, screen } = await import("@testing-library/react");
    const { DiffViewer } = await import("../src/features/incidents/RemediationPanel");

    render(createElement(DiffViewer, { value: "raw unparseable diff text" }));

    expect(screen.getByText("Raw diff output")).toBeInTheDocument();
    expect(screen.queryByText("raw unparseable diff text")).not.toBeInTheDocument();

    const rawHeader = screen.getByRole("button", { name: /raw diff output/i });
    fireEvent.click(rawHeader);

    expect(screen.getByText("raw unparseable diff text")).toBeInTheDocument();
  });
});
