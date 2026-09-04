export type DiffLineKind = "context" | "removed" | "added" | "hunk";

export type DiffLine = {
  kind: DiffLineKind;
  text: string;
  lineNumber: number | null;
};

export type DiffRow = {
  original: DiffLine | null;
  changed: DiffLine | null;
};

export type DiffFile = {
  path: string;
  header: string | null;
  rows: DiffRow[];
};

export type ParsedUnifiedDiff = {
  files: DiffFile[];
  parseable: boolean;
};

type MutableDiffFile = DiffFile & {
  originalPath?: string;
  changedPath?: string;
};

type HunkPosition = {
  originalStart: number;
  changedStart: number;
};

function pathFromHeader(value: string): string {
  const path = value.slice(4).split("\t", 1)[0].trim();
  if (path === "/dev/null") return "New file";
  return path.replace(/^[ab]\//, "");
}

function pathFromGitHeader(value: string): string | null {
  const match = value.match(/^diff --git a\/(.+) b\/(.+)$/);
  return match?.[2] ?? null;
}

function parseHunkPosition(value: string): HunkPosition | null {
  const match = value.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  if (!match) return null;
  return { originalStart: Number(match[1]), changedStart: Number(match[2]) };
}

function createFile(path = "Changed file", header: string | null = null): MutableDiffFile {
  return { path, header, rows: [] };
}

function appendPendingChanges(file: MutableDiffFile, removed: DiffLine[], added: DiffLine[]): void {
  const count = Math.max(removed.length, added.length);
  for (let index = 0; index < count; index += 1) {
    file.rows.push({
      original: removed[index] ?? null,
      changed: added[index] ?? null,
    });
  }
  removed.length = 0;
  added.length = 0;
}

export function parseUnifiedDiff(input: string): ParsedUnifiedDiff {
  const lines = input.replace(/\r\n?/g, "\n").split("\n");
  const files: MutableDiffFile[] = [];
  let current: MutableDiffFile | null = null;
  let hunkPosition: HunkPosition | null = null;
  let hasHunk = false;
  const pendingRemoved: DiffLine[] = [];
  const pendingAdded: DiffLine[] = [];

  const ensureFile = (): MutableDiffFile => {
    if (!current) {
      current = createFile();
      files.push(current);
    }
    return current;
  };

  const flushChanges = (): void => {
    if (current) appendPendingChanges(current, pendingRemoved, pendingAdded);
  };

  for (const line of lines) {
    if (line.startsWith("diff --git ")) {
      flushChanges();
      current = createFile(pathFromGitHeader(line) ?? undefined, line);
      files.push(current);
      hunkPosition = null;
      continue;
    }

    if (line.startsWith("--- ")) {
      flushChanges();
      const file = ensureFile();
      file.originalPath = pathFromHeader(line);
      if (!file.changedPath) file.path = file.originalPath;
      continue;
    }

    if (line.startsWith("+++ ")) {
      const file = ensureFile();
      file.changedPath = pathFromHeader(line);
      file.path = file.changedPath;
      continue;
    }

    if (line.startsWith("@@ ")) {
      flushChanges();
      const file = ensureFile();
      file.rows.push({
        original: { kind: "hunk", text: line, lineNumber: null },
        changed: { kind: "hunk", text: line, lineNumber: null },
      });
      hunkPosition = parseHunkPosition(line);
      hasHunk = hasHunk || hunkPosition !== null;
      continue;
    }

    if (line.startsWith("\\ No newline at end of file")) continue;
    if (!hunkPosition) continue;

    if (line.startsWith(" ")) {
      flushChanges();
      const text = line.slice(1);
      const file = ensureFile();
      file.rows.push({
        original: { kind: "context", text, lineNumber: hunkPosition.originalStart },
        changed: { kind: "context", text, lineNumber: hunkPosition.changedStart },
      });
      hunkPosition.originalStart += 1;
      hunkPosition.changedStart += 1;
      continue;
    }

    if (line.startsWith("-")) {
      pendingRemoved.push({ kind: "removed", text: line.slice(1), lineNumber: hunkPosition.originalStart });
      hunkPosition.originalStart += 1;
      continue;
    }

    if (line.startsWith("+")) {
      pendingAdded.push({ kind: "added", text: line.slice(1), lineNumber: hunkPosition.changedStart });
      hunkPosition.changedStart += 1;
    }
  }

  flushChanges();
  const parsedFiles = files.filter((file) => file.rows.length > 0).map(({ path, header, rows }) => ({ path, header, rows }));
  return { files: parsedFiles, parseable: hasHunk && parsedFiles.length > 0 };
}
