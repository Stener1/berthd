import assert from "node:assert/strict";
import { test } from "node:test";

import { parseTestResult } from "./test-results.ts";

test("Vitest failures keep their test name and file line", () => {
  const result = parseTestResult(` RUN  v3.2.1 /work/shop
 ❯ apps/web/lib/checkout/createOrder.test.ts (5 tests | 1 failed) 18ms
   × reuses an order when a webhook retries 8ms
     apps/web/lib/checkout/createOrder.test.ts:42:11
 Test Files  1 failed | 2 passed (3)
      Tests  1 failed | 8 passed (9)`);
  assert.deepEqual(result, { runner: "Vitest", failed: 1, passed: 8, failures: [{ name: "reuses an order when a webhook retries", file: "apps/web/lib/checkout/createOrder.test.ts", line: 42 }] });
});

test("Playwright and Go failures carry their locations", () => {
  assert.deepEqual(parseTestResult(`  1) [chromium] › e2e/checkout.spec.ts:27:5 › checkout › shows receipt
  1 failed
  4 passed (12.3s)`), { runner: "Playwright", failed: 1, passed: 4, failures: [{ name: "shows receipt", file: "e2e/checkout.spec.ts", line: 27 }] });
  assert.deepEqual(parseTestResult(`=== RUN   TestRetry
--- FAIL: TestRetry (0.00s)
    services/payments/retry_test.go:31: expected one order
FAIL
FAIL  acme/shop/services/payments 0.02s`), { runner: "Go test", failed: 1, failures: [{ name: "TestRetry", file: "services/payments/retry_test.go", line: 31 }] });
});

test("ordinary logs and fragments stay code", () => {
  assert.equal(parseTestResult("The failed tests are being investigated."), undefined);
  assert.equal(parseTestResult("1 failed\n4 passed"), undefined);
  assert.equal(parseTestResult("FAIL\n    foo_test.go:42: nope"), undefined);
});

test("a long Playwright log keeps its summary without overflowing", () => {
  const text = `1) [chromium] › e2e/checkout.spec.ts:27:5 › shows receipt${"\n".repeat(70_000)}1 failed\n4 passed`;
  assert.equal(parseTestResult(text)?.failed, 1);
});
