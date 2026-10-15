// node --experimental-strip-types --test src/lib/code-blocks.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { diffPatch, drawnBlock, tidyFile } from "./code-blocks.ts";

// Which of a reply's code blocks the chat draws (components/conversation/
// code-blocks.tsx), and the patch a diff block hands the renderer.

const GIT = ["di", "ff --git"].join("");

test("a mermaid flowchart draws; other mermaid, and a flowchart with nothing in it, stay as written", () => {
  const flow = drawnBlock("mermaid", "flowchart LR\n  A[Request] --> B{Cached?}\n  B -->|yes| C[Serve]\n  B -->|no| D[(acme-db)]");
  assert.equal(flow?.kind, "flowchart");
  assert.equal(flow?.kind === "flowchart" && flow.chart.nodes.length, 4);
  assert.equal(drawnBlock("mermaid", "graph TD;\nA-->B;")?.kind, "flowchart");
  assert.deepEqual(drawnBlock("mermaid", "sequenceDiagram\n  Alice->>Bob: hi"), { kind: "mermaid" });
  assert.deepEqual(drawnBlock("mermaid", "pie title Pets\n  \"Dogs\" : 3"), { kind: "mermaid" });
  assert.deepEqual(drawnBlock("mermaid", "flowchart TD\n"), { kind: "mermaid" });
});

test("other languages, empty blocks and very long ones aren't drawn", () => {
  assert.equal(drawnBlock("typescript", "const a = 1;"), undefined);
  assert.equal(drawnBlock("text", "+ a\n- b"), undefined);
  assert.equal(drawnBlock("diff", "  \n"), undefined);
  assert.equal(drawnBlock("diff", Array.from({ length: 2100 }, (_, i) => `+${i}`).join("\n")), undefined);
});

test("a patch with its files' headers is files, its hunks recounted", () => {
  const text = `${GIT} a/src/acme.ts b/src/acme.ts\nindex 1a2b3c4..5d6e7f8 100644\n--- a/src/acme.ts\n+++ b/src/acme.ts\n@@ -10,9 +10,9 @@ export function load()\n const a = 1;\n-const b = 2;\n+const b = 3;\n const c = 4;\n`;
  const d = diffPatch(text);
  assert.equal(d?.shape, "files");
  assert.match(d!.patch, /^@@ -10,3 \+10,3 @@ export function load\(\)$/m);
  assert.match(d!.patch, /^index 1a2b3c4\.\.5d6e7f8 100644$/m);
  // patch is accepted as diff's other name.
  assert.equal(drawnBlock("patch", text)?.kind, "diff");
});

test("an empty line inside a hunk is context; between files it isn't", () => {
  const text = "--- a/x.go\n+++ b/x.go\n@@ -1,4 +1,4 @@\n func a() {\n\n-\treturn 1\n+\treturn 2\n\n--- a/y.go\n+++ b/y.go\n@@ -1 +1 @@\n-old\n+new\n";
  const d = diffPatch(text)!;
  assert.equal(d.shape, "files");
  assert.match(d.patch, /^@@ -1,3 \+1,3 @@$/m);
  assert.match(d.patch, /^ func a\(\) \{\n \n-\treturn 1$/m);
  assert.match(d.patch, /^\+\treturn 2\n--- a\/y\.go$/m);
  assert.match(d.patch, /^@@ -1,1 \+1,1 @@$/m);
});

test("in a git patch, a file added without git's first line gets one, new or gone", () => {
  const text = `${GIT} a/src/acme.ts b/src/acme.ts\n--- a/src/acme.ts\n+++ b/src/acme.ts\n@@ -1 +1 @@\n-a\n+b\n--- /dev/null\n+++ b/src/pairs.ts\n@@ -0,0 +1 @@\n+c\n--- a/src/old.ts\n+++ /dev/null\n@@ -1 +0,0 @@\n-d\n`;
  const lines = diffPatch(text)!.patch.split("\n");
  assert.equal(lines.filter((l) => l.startsWith(GIT)).length, 3);
  assert.deepEqual(lines.slice(6, 9), [`${GIT} a/src/pairs.ts b/src/pairs.ts`, "new file mode 100644", "--- /dev/null"]);
  assert.deepEqual(lines.slice(12, 14), [`${GIT} a/src/old.ts b/src/old.ts`, "deleted file mode 100644"]);
});

test("hunks without file headers are numbered but name no file", () => {
  const d = diffPatch("@@ -40,2 +40,3 @@\n retries: 3\n-timeout: 10\n+timeout: 30\n+backoff: 2")!;
  assert.equal(d.shape, "hunks");
  assert.equal(d.patch, "--- a/change\n+++ b/change\n@@ -40,2 +40,3 @@\n retries: 3\n-timeout: 10\n+timeout: 30\n+backoff: 2\n");
});

test("bare +/- lines are one hunk; an unprefixed line is context as written", () => {
  const d = diffPatch("function total() {\n-  return a + b;\n+  return a + b + c;\n }")!;
  assert.equal(d.shape, "lines");
  assert.equal(d.patch, "--- a/change\n+++ b/change\n@@ -1,3 +1,3 @@\n function total() {\n-  return a + b;\n+  return a + b + c;\n }\n");
});

test("a diff block that isn't a diff stays as written", () => {
  // No line added or taken out.
  assert.equal(diffPatch("just some words\nand more"), undefined);
  // Headers with no hunk: a rename, a binary file.
  assert.equal(diffPatch(`${GIT} a/logo.png b/logo.png\nBinary files differ`), undefined);
  assert.equal(diffPatch("--- a/x\n+++ b/x\n"), undefined);
});

test("tidyFile names files as git would", () => {
  const f = (name: string, prevName: string | undefined, type: "change" | "rename-pure" | "rename-changed" | "new" | "deleted" = "rename-changed") => ({ name, prevName, type });
  assert.deepEqual(tidyFile(f("b/src/acme.ts", "a/src/acme.ts")), f("src/acme.ts", undefined, "change"));
  assert.deepEqual(tidyFile(f("b/new.md", "/dev/null")), f("new.md", undefined, "new"));
  assert.deepEqual(tidyFile(f("/dev/null", "a/old.md")), f("old.md", undefined, "deleted"));
  assert.deepEqual(tidyFile(f("b/to.ts", "a/from.ts")), f("to.ts", "from.ts"));
  // diff -u's times after a tab.
  assert.deepEqual(tidyFile(f("acme.c\t2026-01-02 10:00:00", "acme.c\t2026-01-01 10:00:00")), f("acme.c", undefined, "change"));
  // Git's own header names them already: a directory called b stays.
  assert.deepEqual(tidyFile(f("b/readme.md", undefined, "change")), f("b/readme.md", undefined, "change"));
});
