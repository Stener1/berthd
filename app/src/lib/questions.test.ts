// node --experimental-strip-types --test src/lib/questions.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { answersText, type Question } from "./questions.ts";

const qs: Question[] = [
  { question: "How should acme roll out the new checkout?\n1. The old flow stays behind a flag.", header: "Rollout", options: [{ label: "Flag first" }, { label: "All at once" }] },
  { question: "Which checks run first?", header: "Checks", multi: true, options: [{ label: "Unit tests" }, { label: "E2E" }] },
  { question: "Anything else before we start?", options: [{ label: "Nothing" }] },
];

test("copied answers are one line each, under the question's name", () => {
  const text = answersText(qs, [{ picks: [], other: "Flag first, but  staff only\nuntil Monday." }, { picks: ["E2E", "Unit tests"], other: "smoke" }, undefined]);
  assert.equal(text, ["Rollout: Flag first, but staff only until Monday.", "Checks: Unit tests, E2E, smoke", "Anything else before we start?: —"].join("\n"));
});

test("a question without a header is named by its first line", () => {
  assert.equal(answersText([{ ...qs[0], header: undefined }], [{ picks: ["Flag first"] }]), "How should acme roll out the new checkout?: Flag first");
});
