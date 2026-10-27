// Checks that the release build's Content Security Policy lets the app load
// its plugins. Tauri applies the CSP only to the bundled app, never to
// `pnpm tauri dev`, so a directive that blocks plugins breaks only releases:
// v0.3.1 shipped without 'self' in connect-src, so the app could not fetch
// its own builtin-plugins/index.json and a fresh install had no built-ins.
//
//   node scripts/check-csp.mjs
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const conf = JSON.parse(await readFile(new URL("../src-tauri/tauri.conf.json", import.meta.url), "utf8"));
const csp = Object.fromEntries(
  conf.app.security.csp
    .split(";")
    .map((d) => d.trim().split(/\s+/))
    .filter((d) => d[0])
    .map(([name, ...sources]) => [name, sources]),
);
const has = (directive, source, why) => assert.ok((csp[directive] ?? csp["default-src"] ?? []).includes(source), `${directive} needs ${source}: ${why}`);

// host.ts fetches the built-ins' index and modules from the app's own files.
has("connect-src", "'self'", "the app fetches its built-in plugins");
// User plugins come from the agent.
has("connect-src", "http://127.0.0.1:1378", "the app lists and fetches your plugins from the agent");
// Every plugin is imported from a blob: URL; its imports, the shims and lazy
// chunks come from the app's own files.
has("script-src", "blob:", "plugins are imported from blob: URLs");
has("script-src", "'self'", "plugins import the shims and their lazy chunks");
// The diff renderer (components/diff, shared with the Diff plugin)
// highlights in module workers started from the app's own files, which
// load their chunks from there too. Without worker-src, script-src rules.
const worker = csp["worker-src"] ?? csp["script-src"] ?? csp["default-src"] ?? [];
assert.ok(worker.includes("'self'"), "worker-src (or script-src) needs 'self': the diff renderer's highlighting workers");
// Chat backgrounds: your own pictures show as blob: URLs in Settings, and
// the frosted grain is a data: SVG.
has("img-src", "blob:", "Settings shows your chat backgrounds from blob: URLs");
has("img-src", "data:", "the chat background's frosted grain is a data: image");
// A Markdown file's Preview shows a README's badges and screenshots from
// the web; the worktree's own pictures are blob: URLs.
has("img-src", "https:", "a Markdown file's Preview shows pictures from the web");
// Terminals: ghostty-web fetches its WebAssembly from a data: URL and
// compiles it (src/lib/terminal.ts).
has("connect-src", "data:", "ghostty-web fetches its WebAssembly from a data: URL");
has("script-src", "'wasm-unsafe-eval'", "ghostty-web compiles WebAssembly");

// Browser panes are WKWebViews, and App Transport Security refuses plain
// http in a bundled app, which `pnpm tauri dev` never is: v0.3.5's panes
// stayed blank on every http://WT.LOC.BOX.localhost:1377/ page (-1022).
// src-tauri/Info.plist, which Tauri merges into the bundle's, allows
// localhost and its subdomains.
const plist = await readFile(new URL("../src-tauri/Info.plist", import.meta.url), "utf8");
const localhost = /<key>NSExceptionDomains<\/key>\s*<dict>\s*<key>localhost<\/key>\s*<dict>([\s\S]*?)<\/dict>/.exec(plist)?.[1] ?? "";
for (const key of ["NSIncludesSubdomains", "NSExceptionAllowsInsecureHTTPLoads"]) {
  assert.match(localhost, new RegExp(`<key>${key}</key>\\s*<true/>`), `Info.plist: ATS needs ${key} for localhost, or browser panes can't open http://*.localhost pages`);
}
console.log("check-csp: ok");
