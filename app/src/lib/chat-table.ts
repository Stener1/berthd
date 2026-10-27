import { verdict } from "./art/table-data.ts";
import type { BerthChart } from "./art/chart-spec.ts";

export interface ChatTableData {
  head: string[];
  rows: string[][];
}

export interface TablePlan {
  numeric: boolean[];
  status: number;
  failures: number;
  chart?: BerthChart;
}

export function tableNumber(value: string): number | undefined {
  const s = value.trim();
  if (!/^[-+]?\$?(?:\d+|\d{1,3}(?:,\d{3})+)(?:\.\d+)?%?$/.test(s)) return undefined;
  const n = Number(s.replace(/[$,%]/g, ""));
  return Number.isFinite(n) ? n : undefined;
}

export function planChatTable(data: ChatTableData): TablePlan {
  const { head, rows } = data;
  const status = head.findIndex((h) => /^(status|result|outcome|state)$/i.test(h.trim()));
  const failures = status < 0 ? 0 : rows.filter((r) => verdict(r[status] ?? "") === "fail").length;
  const numeric = head.map((_, i) => rows.length > 0 && rows.every((r) => tableNumber(r[i] ?? "") !== undefined));
  const category = head.findIndex((_, i) => i !== status && !numeric[i]);
  const columns = head.map((_, i) => i).filter((i) => numeric[i]).slice(0, 3);
  let chart: BerthChart | undefined;
  if (category >= 0 && columns.length && rows.length >= 2 && rows.length <= 16) {
    chart = {
      $schema: "berth.chart/v1",
      type: "bar",
      x: "label",
      y: columns.map((i) => ({ key: `v${i}`, label: head[i] })),
      data: rows.map((r) => Object.fromEntries([["label", r[category] ?? ""], ...columns.map((i) => [`v${i}`, tableNumber(r[i] ?? "") ?? 0])])),
    };
  }
  return { numeric, status, failures, chart };
}

export function shownRows(data: ChatTableData, plan: TablePlan, sort?: { index: number; descending: boolean }, failuresOnly = false): string[][] {
  let rows = failuresOnly && plan.status >= 0 ? data.rows.filter((r) => verdict(r[plan.status] ?? "") === "fail") : data.rows;
  if (sort) {
    const { index, descending } = sort;
    const dir = descending ? -1 : 1;
    rows = [...rows].sort((a, b) => {
      const x = a[index] ?? "";
      const y = b[index] ?? "";
      const nx = tableNumber(x);
      const ny = tableNumber(y);
      return (nx !== undefined && ny !== undefined ? nx - ny : x.localeCompare(y, undefined, { numeric: true })) * dir;
    });
  }
  return rows;
}
