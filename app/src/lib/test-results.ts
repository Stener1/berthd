// Recognize a few common test reporters in a fenced output block. A loose
// "failed" word is not enough: ordinary logs and prose stay code. Unknown
// formats keep their original source in the reply.
export interface TestFailure {
  name: string;
  file?: string;
  line?: number;
}

export interface TestResult {
  runner: "Go test" | "Vitest" | "Playwright";
  passed?: number;
  failed: number;
  failures: TestFailure[];
}

const location = (s: string): { file: string; line: number } | undefined => {
  const m = /(?:^|\s|›\s)([\w./@[\]-]+\.(?:test|spec)\.[jt]sx?|[\w./@[\]-]+_test\.go):(\d+)(?::\d+)?/.exec(s);
  return m ? { file: m[1], line: Number(m[2]) } : undefined;
};

const count = (line: string, word: "failed" | "passed") => Number(new RegExp(`\\b(\\d+)\\s+${word}\\b`, "i").exec(line)?.[1] ?? 0);

export function parseTestResult(text: string): TestResult | undefined {
  if (text.length > 100_000) return undefined;
  const lines = text.split(/\r?\n/);

  // Vitest puts the totals on a "Tests" line, and marks failed cases ×.
  const vitest = lines.find((line) => /^\s*Tests\s+.*\b(?:failed|passed)\b/.test(line));
  if (vitest && lines.some((line) => /\b(?:Test Files|RUN\s+v\d|vitest)\b/i.test(line))) {
    const failures: TestFailure[] = [];
    for (let i = 0; i < lines.length; i++) {
      const m = /^\s*[×✕]\s+(.+?)(?:\s+\d+ms)?\s*$/.exec(lines[i]);
      if (!m) continue;
      const at = lines.slice(i + 1, i + 7).map(location).find(Boolean);
      failures.push({ name: m[1], ...at });
    }
    const failed = count(vitest, "failed");
    const passed = count(vitest, "passed");
    if (!failed && !passed) return undefined;
    return { runner: "Vitest", failed, passed, failures: failures.slice(0, 20) };
  }

  // Playwright's numbered failures include the project, path and line.
  const playwright = lines.some((line) => /^\s*\d+\)\s+\[[^\]]+\]\s*›/.test(line));
  if (playwright) {
    const failures = lines.flatMap((line): TestFailure[] => {
      const m = /^\s*\d+\)\s+\[[^\]]+\]\s*›\s*(.+)$/.exec(line);
      if (!m) return [];
      const parts = m[1].split("›").map((x) => x.trim());
      return [{ name: parts.at(-1) || "Failed test", ...location(m[1]) }];
    });
    const failed = lines.reduce((n, line) => /^\s*\d+ failed\b/.test(line) ? Math.max(n, count(line, "failed")) : n, failures.length);
    const passed = lines.reduce((n, line) => /^\s*\d+ passed\b/.test(line) ? Math.max(n, count(line, "passed")) : n, 0);
    return { runner: "Playwright", failed, passed, failures: failures.slice(0, 20) };
  }

  // go test -v names each case; failures generally put file:line below it.
  if (lines.some((line) => /^--- FAIL: \S+/.test(line)) && lines.some((line) => /^FAIL(?:\s|$)/.test(line))) {
    const failures: TestFailure[] = [];
    for (let i = 0; i < lines.length; i++) {
      const m = /^--- FAIL: (\S+)/.exec(lines[i]);
      if (!m) continue;
      const at = lines.slice(i + 1, i + 8).map(location).find(Boolean);
      failures.push({ name: m[1], ...at });
    }
    const passed = lines.filter((line) => /^--- PASS: \S+/.test(line)).length;
    return { runner: "Go test", failed: failures.length, ...(passed ? { passed } : {}), failures: failures.slice(0, 20) };
  }
  return undefined;
}
