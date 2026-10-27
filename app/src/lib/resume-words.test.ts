// node --experimental-strip-types --test src/lib/resume-words.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { resumeWords } from "./resume-words.ts";

test("/resume and /continue open the picker, filtered by what follows", () => {
  assert.equal(resumeWords("/resume"), "");
  assert.equal(resumeWords("  /resume  "), "");
  assert.equal(resumeWords("/continue"), "");
  assert.equal(resumeWords("/resume release plan"), "release plan");
  assert.equal(resumeWords("/resume 3f2a9c1e-0b4d-4e8a-9f1c-2d7e5b6a8c90"), "3f2a9c1e-0b4d-4e8a-9f1c-2d7e5b6a8c90");
});

test("anything else goes to the agent", () => {
  assert.equal(resumeWords("/resumes"), undefined);
  assert.equal(resumeWords("/review"), undefined);
  assert.equal(resumeWords("please /resume the work"), undefined);
  assert.equal(resumeWords("resume"), undefined);
});
