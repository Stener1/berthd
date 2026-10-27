import type { BerthPluginContext } from "@berth/plugin";

import type { Capabilities, Connection, Database, Fault } from "./api";
import type { Engine } from "./engines/index.ts";
import type { Writes } from "./lock";

// What the browser and the console are given: one worktree's connection on
// one box, with what its engine can do.
export interface Ctx {
  berth: BerthPluginContext;
  api: Database;
  box: string;
  // The worktree's name in the helper, and in words.
  source: string;
  worktree: string;
  conn: Connection;
  caps: Capabilities;
  engine: Engine;
  writes: Writes;
  // False while the panel is out of sight: nothing is read then.
  active: boolean;
  // A request failed: if the helper went off, the panel says so.
  onFault(fault: Fault): void;
}
