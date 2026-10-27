import assert from "node:assert/strict";
import { test } from "node:test";

import { planChatTable, shownRows, tableNumber } from "./chat-table.ts";

test("a numeric Markdown table can become a chart", () => {
  const data = { head: ["Endpoint", "Before", "After"], rows: [["Search", "1,240", "410"], ["Cart", "880", "520"]] };
  const plan = planChatTable(data);
  assert.deepEqual(plan.numeric, [false, true, true]);
  assert.deepEqual(plan.chart?.y, [{ key: "v1", label: "Before" }, { key: "v2", label: "After" }]);
  assert.deepEqual(plan.chart?.data?.[0], { label: "Search", v1: 1240, v2: 410 });
  assert.equal(tableNumber("$1,240.50"), 1240.5);
  assert.equal(tableNumber("34%"), 34);
  assert.equal(tableNumber("about 34"), undefined);
});

test("a results table filters failures and sorts numbers", () => {
  const data = { head: ["Test", "Status", "Time"], rows: [["checkout", "pass", "100"], ["retry", "fail", "20"], ["refund", "fail", "5"]] };
  const plan = planChatTable(data);
  assert.equal(plan.failures, 2);
  assert.deepEqual(shownRows(data, plan, { index: 2, descending: true }, true).map((r) => r[0]), ["retry", "refund"]);
  assert.deepEqual(shownRows(data, plan, { index: 0, descending: false }).map((r) => r[0]), ["checkout", "refund", "retry"]);
});

test("large and nonnumeric tables keep the table view", () => {
  const data = { head: ["Name", "Count"], rows: Array.from({ length: 17 }, (_, i) => [`row ${i}`, String(i)]) };
  assert.equal(planChatTable(data).chart, undefined);
  assert.equal(planChatTable({ head: ["Name", "Note"], rows: [["A", "ok"], ["B", "slow"]] }).chart, undefined);
});
