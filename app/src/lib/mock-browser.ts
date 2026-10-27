import type { AgentDevtools } from "@/lib/devtools-model";
import { mockAgentDevtools } from "@/lib/mock-devtools";

// The demo's agent browser: the checkout-fix worktree on devl has one open,
// at the default 1920×1080, cast as a drawing of the shop's cart page, with
// two shots for Review. Its box has tabs (browser.tabs): besides the main
// one, three teammates of the lead agent each have their own (signup-a,
// signup-b, onboarding), each cast as a page of its own. The main tab is the
// one used last, so the view opens on the cart as before.
//
// window.__berthMockBrowser stands in for agents, for tests and
// screenshots: .resize("390x844", 3, tab?) does what `berthd browser
// resize` would, .opened() the event `berthd browser open` sends as its
// browser starts, .navigate(tab, path) an agent moving its tab to another
// page, and .openTab(name) / .closeTab(name) an agent's first command on a
// tab and its closing.

interface Size {
  width: number;
  height: number;
  scale: number;
}

interface Tab {
  name: string;
  path: string;
  title: string;
  // The page the drawing shows, and its colour.
  heading: string;
  accent: string;
  // Its own size; the browser's default otherwise.
  size?: Size;
  used: number;
  busyUntil: number;
}

const ORIGIN = "http://checkout-fix.shop.devl.localhost:1377";
const WT = { location: "shop", name: "checkout-fix", path: "/home/me/work/shop-checkout-fix" };

let size: Size = { width: 1920, height: 1080, scale: 1 };
const label = (v: Size) => `${v.width}×${v.height}${v.scale !== 1 ? ` @${v.scale}x` : ""}`;

const t0 = Date.now();
const tabs: Tab[] = [
  { name: "", path: "/cart", title: "Cart · acme shop", heading: "Cart", accent: "#6366f1", used: t0 - 1000, busyUntil: 0 },
  { name: "signup-a", path: "/signup?plan=a", title: "Sign up · acme shop", heading: "Sign up: plan A", accent: "#0ea5e9", used: t0 - 40_000, busyUntil: 0 },
  { name: "signup-b", path: "/signup?plan=b", title: "Sign up · acme shop", heading: "Sign up: plan B", accent: "#f59e0b", used: t0 - 25_000, busyUntil: 0 },
  { name: "onboarding", path: "/welcome", title: "Welcome · acme shop", heading: "Welcome aboard", accent: "#10b981", size: { width: 390, height: 844, scale: 3 }, used: t0 - 60_000, busyUntil: 0 },
];

const tabSize = (t: Tab) => t.size ?? size;
const find = (name: string) => tabs.find((t) => t.name === name);

// The drawing is laid out at the page's own width, as the shop's page
// would be (a column at most 1100 wide), and drawn at the page's scale.
const svg = (count: number, v: Size = size, t: Tab = tabs[0]) => {
  const vw = v.width;
  const vh = v.height;
  const cw = Math.min(1100, vw - 48);
  const x = Math.round((vw - cw) / 2);
  const narrow = vw < 600;
  const main = t.name === "";
  const body = main
    ? `<rect x="${x}" y="180" width="${Math.min(760, cw)}" height="96" rx="12" fill="#fff" stroke="#e2e8f0"/>
<text x="${x + 24}" y="236" font-family="system-ui" font-size="22" fill="#334155">Ceramic mug × ${count}</text>
<rect x="${x}" y="320" width="${narrow ? cw : 220}" height="56" rx="10" fill="${t.accent}"/><text x="${narrow ? x + cw / 2 - 44 : x + 36}" y="356" font-family="system-ui" font-size="22" fill="#fff">Checkout</text>`
    : `<rect x="${x}" y="180" width="${Math.min(520, cw)}" height="48" rx="8" fill="#fff" stroke="#e2e8f0"/><text x="${x + 16}" y="211" font-family="system-ui" font-size="18" fill="#94a3b8">you@acme.test</text>
<rect x="${x}" y="244" width="${Math.min(520, cw)}" height="48" rx="8" fill="#fff" stroke="#e2e8f0"/><text x="${x + 16}" y="275" font-family="system-ui" font-size="18" fill="#94a3b8">${t.path}</text>
<rect x="${x}" y="320" width="${narrow ? cw : 220}" height="56" rx="10" fill="${t.accent}"/><text x="${narrow ? x + cw / 2 - 30 : x + 36}" y="356" font-family="system-ui" font-size="22" fill="#fff">Continue</text>`;
  const where = main ? `The agent's browser on devl, ${label(v)}` : `The agent's browser on devl, tab ${t.name}, ${label(v)}`;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${vw * v.scale}" height="${vh * v.scale}" viewBox="0 0 ${vw} ${vh}">
<rect width="${vw}" height="${vh}" fill="#f8fafc"/><rect width="${vw}" height="64" fill="#0f172a"/><rect width="${vw}" height="4" y="60" fill="${t.accent}"/>
<text x="${x}" y="41" font-family="system-ui" font-size="22" fill="#fff">acme shop</text>
<text x="${x}" y="140" font-family="system-ui" font-size="${narrow ? 30 : 40}" font-weight="700" fill="#0f172a">${t.heading}</text>
${body}
<text x="${x}" y="440" font-family="system-ui" font-size="${narrow ? 14 : 18}" fill="#64748b">${where}</text></svg>`;
};

export const mockShotSvg = () => svg(2);

const b64 = (s: string) => btoa(unescape(encodeURIComponent(s)));

// The tab a route asks for: ?tab=NAME, none for main.
const tabOf = (path: string) => new URLSearchParams(path.split("?")[1] ?? "").get("tab") ?? "";

export async function mockScreencast(path: string, onValue: (v: unknown) => void, signal?: AbortSignal) {
  const name = tabOf(path);
  if (!find(name)) throw new Error(`no tab ${name} in this worktree's browser`);
  for (let i = 1; !signal?.aborted; i++) {
    const t = find(name);
    // Its tab closed: the box ends the cast.
    if (!t) return;
    const v = tabSize(t);
    onValue({ data: b64(svg((i % 3) + 1, v, t)), mime: "image/svg+xml", w: v.width, h: v.height, scale: v.scale, url: ORIGIN + t.path });
    await new Promise((r) => setTimeout(r, name ? 600 : 1200));
  }
}

const tabStatus = (t: Tab, now: number) => ({
  name: t.name,
  url: ORIGIN + t.path,
  title: t.title,
  last_used: new Date(t.used).toISOString(),
  watchers: 0,
  viewport: tabSize(t),
  size: label(tabSize(t)),
  ...(t.busyUntil > now ? { busy: true } : {}),
});

const tabDevtools = (t: Tab): AgentDevtools =>
  t.name === "" ? mockAgentDevtools : { running: true, url: ORIGIN + t.path, console: [{ seq: 1, level: "log", text: `${t.name}: ${t.heading} is ready`, count: 1 }], failures: [] };

export function browserCall(box: string, method: string, full: string): unknown {
  if (box !== "devl" || !full.includes("/browser/")) return undefined;
  const [path] = full.split("?");
  const now = Date.now();
  if (method === "GET" && path === "worktrees/shop/checkout-fix/browser/status") {
    const main = tabs[0];
    const list = tabs.map((t) => tabStatus(t, now));
    return {
      running: true,
      text: `open: ${ORIGIN}${main.path} · ${label(size)} (312 MB) · ${tabs.length} tabs`,
      viewport: size,
      size: label(size),
      tabs: list,
      status: { url: ORIGIN + main.path, rss_bytes: 312 << 20, viewport: size, size: label(size), tabs: list },
    };
  }
  if (method === "GET" && path.endsWith("/browser/status")) return { running: false, text: "no browser open; it opens at 1920×1080", viewport: { width: 1920, height: 1080, scale: 1 }, size: "1920×1080" };
  if (method === "GET" && path === "worktrees/shop/checkout-fix/browser/devtools") {
    const t = find(tabOf(full));
    return t ? tabDevtools(t) : { running: false, console: [], failures: [] };
  }
  if (method === "GET" && path.endsWith("/browser/devtools")) return { running: false, console: [], failures: [] };
  return undefined;
}

let emit: ((e: { type: string; box: string; origin?: string; data?: Record<string, unknown> }) => void) | undefined;

// wireBrowserMock gives the mock its event stream.
export function wireBrowserMock(e: typeof emit) {
  emit = e;
}

if (typeof window !== "undefined") {
  (window as unknown as { __berthMockBrowser: unknown }).__berthMockBrowser = {
    opened() {
      emit?.({ type: "browser.opened", box: "devl", origin: "browser", data: { ...WT } });
    },
    resize(wxh: string, scale = 1, tab?: string) {
      const [width, height] = wxh.split("x").map(Number);
      const t = tab === undefined ? undefined : find(tab);
      if (t) t.size = { width, height, scale };
      else size = { width, height, scale };
    },
    navigate(tab: string, path: string) {
      const t = find(tab);
      if (!t) return;
      t.path = path;
      t.used = Date.now();
      t.busyUntil = t.used + 2000;
      emit?.({ type: "browser.navigated", box: "devl", origin: "browser", data: { ...WT, tab, url: ORIGIN + path } });
    },
    openTab(name: string, path = "/") {
      if (find(name)) return;
      tabs.push({ name, path, title: "acme shop", heading: name, accent: "#a855f7", used: Date.now(), busyUntil: 0 });
      emit?.({ type: "browser.tab.opened", box: "devl", origin: "browser", data: { ...WT, tab: name } });
    },
    closeTab(name: string) {
      const i = tabs.findIndex((t) => t.name === name);
      if (i <= 0) return;
      tabs.splice(i, 1);
      emit?.({ type: "browser.tab.closed", box: "devl", origin: "browser", data: { ...WT, tab: name } });
    },
  };
}
