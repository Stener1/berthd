// src/index.tsx
import { useBoxes, useWidgetData } from "@berth/plugin";
import { Badge, Button, Empty, EmptyDescription, EmptyHeader, EmptyTitle, Icon, Spinner, WidgetEmpty, WidgetRow, WidgetSkeleton } from "@berth/plugin/ui";
import { useEffect, useState } from "react";
import { jsx, jsxs } from "react/jsx-runtime";
function useServices(berth) {
  const boxes = useBoxes();
  const online = boxes.filter((b) => b.state === "online").map((b) => b.name);
  const key = online.join(",");
  const [rows, setRows] = useState();
  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      const all = await Promise.all(
        online.map(
          (box) => berth.api.services(box).then(
            (s) => s.map((x) => ({ ...x, box })),
            () => []
          )
        )
      );
      if (!cancelled) setRows(all.flat());
    };
    void load();
    const timer = setInterval(() => !document.hidden && void load(), 1e4);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [key, berth]);
  return rows;
}
function PortsScreen({ berth }) {
  const rows = useServices(berth);
  return /* @__PURE__ */ jsx("div", { children: !rows ? /* @__PURE__ */ jsx(Spinner, {}) : rows.length === 0 ? /* @__PURE__ */ jsx(Empty, { className: "mt-10", children: /* @__PURE__ */ jsxs(EmptyHeader, { children: [
    /* @__PURE__ */ jsx(EmptyTitle, { children: "Nothing is listening" }),
    /* @__PURE__ */ jsx(EmptyDescription, { children: "Start a dev server in a worktree and it shows up here." })
  ] }) }) : /* @__PURE__ */ jsx("ul", { className: "divide-y rounded-lg border", children: rows.map((r) => /* @__PURE__ */ jsxs("li", { className: "flex items-center gap-3 px-3 py-2 text-sm", children: [
    /* @__PURE__ */ jsx("span", { className: "w-14 font-mono", children: r.port }),
    /* @__PURE__ */ jsx("span", { className: "font-medium", children: r.worktree }),
    /* @__PURE__ */ jsxs("span", { className: "text-muted-foreground", children: [
      r.location,
      " \xB7 ",
      r.box
    ] }),
    r.main && /* @__PURE__ */ jsx(Badge, { variant: "outline", children: "main" }),
    /* @__PURE__ */ jsx("span", { className: "text-muted-foreground text-xs", children: r.process }),
    /* @__PURE__ */ jsxs(Button, { className: "ml-auto", size: "xs", variant: "outline", onClick: () => berth.openUrl(berth.api.serviceUrl(r.box, r.port)), children: [
      /* @__PURE__ */ jsx(Icon, { name: "ExternalLink" }),
      "Open"
    ] })
  ] }, `${r.box}:${r.port}`)) }) });
}
function PortsHere({ berth, box, path }) {
  const rows = useServices(berth)?.filter((r) => r.box === box && r.path === path);
  if (!rows?.length) return /* @__PURE__ */ jsx("p", { className: "px-2 text-muted-foreground text-xs", children: "No ports open here yet." });
  return /* @__PURE__ */ jsx("div", { className: "flex flex-wrap gap-1.5 px-2", children: rows.map((r) => /* @__PURE__ */ jsxs(Button, { size: "xs", variant: "outline", onClick: () => berth.openBrowser(berth.api.serviceUrl(r.box, r.port)), children: [
    /* @__PURE__ */ jsx(Icon, { name: "Radio" }),
    r.port,
    r.process && /* @__PURE__ */ jsx("span", { className: "text-muted-foreground", children: r.process })
  ] }, r.port)) });
}
function PortsWidget({ berth, size }) {
  const online = useBoxes().filter((b) => b.state === "online").map((b) => b.name);
  const { data } = useWidgetData(
    `ports:${online.join(",")}`,
    async () => (await Promise.all(online.map((box) => berth.api.services(box).then((s) => s.map((x) => ({ ...x, box })), () => [])))).flat(),
    { every: 3e4 }
  );
  if (!data) return /* @__PURE__ */ jsx(WidgetSkeleton, { rows: 3 });
  if (!data.length) return /* @__PURE__ */ jsx(WidgetEmpty, { scene: "dock", title: "Nothing is listening", compact: true });
  return /* @__PURE__ */ jsx("div", { children: data.slice(0, size === "t" ? 8 : 3).map((r) => /* @__PURE__ */ jsxs(WidgetRow, { onClick: () => berth.openUrl(berth.api.serviceUrl(r.box, r.port)), children: [
    /* @__PURE__ */ jsxs("span", { className: "w-12 shrink-0 font-mono text-xs", children: [
      ":",
      r.port
    ] }),
    /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1 truncate", children: r.worktree }),
    /* @__PURE__ */ jsx("span", { className: "text-muted-foreground text-xs", children: r.box })
  ] }, `${r.box}:${r.port}`)) });
}
function activate(berth) {
  berth.addHomeWidget({ id: "ports", title: "Hello ports", description: "Every port listening on every box. From the hello-ports example plugin.", icon: "Radio", sizes: ["s", "m", "t"], category: "Fleet", source: "Each online box's services, every 30s while on screen", Component: PortsWidget });
  berth.addScreen({ id: "ports", title: "Hello ports", description: "Everything listening in a worktree, on every box. From the hello-ports plugin.", Component: PortsScreen });
  berth.addSidebarItem({ id: "ports", title: "Hello ports", icon: "Radio", screen: "ports" });
  berth.addWorktreeSection({ id: "ports", title: "Hello ports", Component: PortsHere });
  berth.addCommand({ id: "ports", title: "Show dev servers", group: "Hello ports", run: () => berth.openScreen("ports") });
  berth.on("worktree.created", (e) => berth.notify("New worktree", `${String(e.data?.name ?? "")} on ${e.box ?? "a box"}`));
}
export {
  activate as default
};
