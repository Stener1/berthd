// The bounce page for "Review in Shipyard" buttons (index.html): its script
// run against stand-ins for the page, so it tests in Node with no browser.
// Run from app/ with the app's unit tests (pnpm test).

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import vm from "node:vm";

import { parseReviewLink } from "../../app/src/lib/review-link.ts";

const here = new URL(".", import.meta.url);
const html = readFileSync(new URL("index.html", here), "utf8");
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
const script = scripts[0];

// run loads the page at ?search and says what it did: where it sent the
// browser, what each element shows, and the page's title.
function run(search) {
  const els = new Map();
  const startHidden = new Set(["ok", "bad", "row-as", "row-path"]);
  const el = (id) => {
    if (!els.has(id)) {
      els.set(id, {
        id,
        hidden: startHidden.has(id),
        textContent: "",
        attrs: {},
        setAttribute(k, v) {
          this.attrs[k] = String(v);
        },
        set innerHTML(_) {
          throw new Error("the page wrote HTML");
        },
      });
    }
    return els.get(id);
  };
  const location = {
    search,
    sent: undefined,
    set href(v) {
      this.sent = v;
    },
    get href() {
      return `https://berthd.app/review${search}`;
    },
    assign(v) {
      this.sent = v;
    },
    replace(v) {
      this.sent = v;
    },
  };
  const document = { title: "Review in Shipyard", getElementById: el };
  vm.runInNewContext(script, { document, location, URL });
  return { sent: location.sent, el, title: document.title };
}

const opens = (search, link) => {
  const r = run(search);
  assert.equal(r.sent, link, search);
  assert.equal(r.el("open").attrs.href, link);
  assert.equal(r.el("ok").hidden, false);
  assert.equal(r.el("bad").hidden, true);
  return r;
};

// refuses: no deep link anywhere, the reason shown, and nothing of the
// query on the page.
const refuses = (search) => {
  const r = run(search);
  assert.equal(r.sent, undefined, `${search} opened ${r.sent}`);
  assert.equal(r.el("open").attrs.href, undefined, search);
  assert.equal(r.el("ok").hidden, true, search);
  assert.equal(r.el("bad").hidden, false, search);
  const why = r.el("why").textContent;
  assert.match(why, /^It |^Part of it |^Its |^Who to log in as /, search);
  for (const bit of ["<", "script", "javascript", "evil", "alert"]) {
    assert.ok(!why.includes(bit) && !r.title.includes(bit), `${search} put ${bit} on the page`);
  }
  return r;
};

// review-link.ts knows as and path once review-login has merged; until then
// only links without them are compared.
const appKnowsLogin = !!parseReviewLink("berth://review?repo=acme/shop&pr=1&as=pro@acme.test")?.as;

test("a plain review link opens exactly that berth:// link", () => {
  const r = opens("?repo=acme/shop&pr=42", "berth://review?repo=acme/shop&pr=42");
  assert.equal(r.el("ok-title").textContent, "Opening #42 of acme/shop in Shipyard…");
  assert.equal(r.el("f-repo").textContent, "acme/shop");
  assert.equal(r.el("f-pr").textContent, "#42");
  assert.equal(r.el("github").attrs.href, "https://github.com/acme/shop/pull/42");
  assert.equal(r.el("row-as").hidden, true);
  assert.equal(r.el("row-path").hidden, true);
  assert.deepEqual(parseReviewLink(r.sent), { repo: "acme/shop", pr: 42 });
  opens("?pr=7&repo=Acme-Labs/shop.web_2", "berth://review?repo=Acme-Labs/shop.web_2&pr=7");
  opens("?repo=acme/shop&pr=2147483647", "berth://review?repo=acme/shop&pr=2147483647");
  opens("?repo=acme/shop&pr=42&sha=ABCDEF1", "berth://review?repo=acme/shop&pr=42&sha=abcdef1");
});

test("as and path go through, checked and escaped again", () => {
  const cases = [
    ["?repo=acme/shop&pr=42&as=pro%40acme.test&path=%2Fbilling", "berth://review?repo=acme/shop&pr=42&as=pro@acme.test&path=/billing", "pro@acme.test", "/billing"],
    ["?repo=acme/shop&pr=42&as=pro@acme.test&path=/billing", "berth://review?repo=acme/shop&pr=42&as=pro@acme.test&path=/billing", "pro@acme.test", "/billing"],
    ["?repo=acme/shop&pr=42&as=qa%2Bteam%40acme.test", "berth://review?repo=acme/shop&pr=42&as=qa%2Bteam@acme.test", "qa+team@acme.test", undefined],
    ["?repo=acme/shop&pr=42&path=%2Fbilling%3Ftab%3Dplans%26x%3D1", "berth://review?repo=acme/shop&pr=42&path=/billing%3Ftab%3Dplans%26x%3D1", undefined, "/billing?tab=plans&x=1"],
    ["?repo=acme/shop&pr=42&path=%2Fsearch%3Fq%3D%253Cb%253E", "berth://review?repo=acme/shop&pr=42&path=/search%3Fq%3D%253Cb%253E", undefined, "/search?q=%3Cb%3E"],
    ["?repo=acme/shop&pr=42&sha=abc1234&as=admin%40acme.test&path=%2F", "berth://review?repo=acme/shop&pr=42&sha=abc1234&as=admin@acme.test&path=/", "admin@acme.test", "/"],
  ];
  for (const [search, link, as, path] of cases) {
    const r = opens(search, link);
    assert.equal(r.el("row-as").hidden, !as, search);
    assert.equal(r.el("row-path").hidden, !path, search);
    if (as) assert.equal(r.el("f-as").textContent, as);
    if (path) assert.equal(r.el("f-path").textContent, path);
    if (appKnowsLogin) {
      const got = parseReviewLink(r.sent);
      assert.equal(got?.as, as, search);
      assert.equal(got?.path, path, search);
    }
    // The link opened has as and path as they were given.
    const q = new URLSearchParams(r.sent.slice("berth://review?".length));
    assert.equal(q.get("as") ?? undefined, as);
    assert.equal(q.get("path") ?? undefined, path);
  }
});

test("junk and injection open nothing and show none of it", () => {
  const long = "a".repeat(245);
  for (const search of [
    "",
    "?",
    "?repo=acme/shop",
    "?pr=1",
    "?repo=acme/shop&pr=0",
    "?repo=acme/shop&pr=01",
    "?repo=acme/shop&pr=-1",
    "?repo=acme/shop&pr=1e3",
    "?repo=acme/shop&pr=2147483648",
    "?repo=acme/shop&pr=1%20",
    "?repo=../etc&pr=1",
    "?repo=acme/..&pr=1",
    "?repo=acme/shop/extra&pr=1",
    "?repo=%3Cscript%3Ealert(1)%3C%2Fscript%3E&pr=1",
    "?repo=<script>alert(1)</script>&pr=1",
    "?repo=javascript:alert(1)&pr=1",
    "?repo=acme/shop&pr=1&pr=2",
    "?repo=acme/shop&repo=evil/shop&pr=1",
    "?repo=acme/shop&pr=1&next=//evil.example",
    "?repo=acme/shop&pr=1&url=javascript:alert(1)",
    "?repo=acme/shop&pr=1&&",
    "?repo=acme/shop&pr=1&=x",
    "?repo=acme/shop&pr=1&sha",
    "?repo=acme/shop&pr=1&sha=xyz1234",
    "?repo=acme/shop&pr=1&sha=abc",
    "?repo=acme/shop&pr=1&as=%3Cscript%3E%40acme.test",
    "?repo=acme/shop&pr=1&as=javascript:alert(1)",
    "?repo=acme/shop&pr=1&as=pro%40acme",
    "?repo=acme/shop&pr=1&as=pro%40acme.test%0A",
    "?repo=acme/shop&pr=1&as=.pro%40acme.test",
    "?repo=acme/shop&pr=1&as=pro%20x%40acme.test",
    `?repo=acme/shop&pr=1&as=${long}%40acme.test`,
    `?repo=acme/shop&pr=1&as=${"a".repeat(65)}%40acme.test`,
    "?repo=acme/shop&pr=1&path=%2F%2Fevil.example",
    "?repo=acme/shop&pr=1&path=//evil.example",
    "?repo=acme/shop&pr=1&path=%2F%252F%252Fevil.example",
    "?repo=acme/shop&pr=1&path=%2F%25252F%25252Fevil.example",
    "?repo=acme/shop&pr=1&path=https%3A%2F%2Fevil.example",
    "?repo=acme/shop&pr=1&path=%2Fx%3Fnext%3Dhttps%3A%2F%2Fevil.example",
    "?repo=acme/shop&pr=1&path=javascript%3Aalert(1)",
    "?repo=acme/shop&pr=1&path=%2F%5Cevil.example",
    "?repo=acme/shop&pr=1&path=%2F%255Cevil.example",
    "?repo=acme/shop&pr=1&path=%2Fx%0Ay",
    "?repo=acme/shop&pr=1&path=%2Fx%2509y",
    "?repo=acme/shop&pr=1&path=%2Fa+b",
    "?repo=acme/shop&pr=1&path=evil",
    "?repo=acme/shop&pr=1&path=",
    `?repo=acme/shop&pr=1&path=%2F${"a".repeat(512)}`,
    // Each part fits, but the link is longer than the box takes.
    `?repo=acme/shop&pr=1&as=pro%40acme.test&path=%2F${"%3F".repeat(170)}`,
    "?repo=acme/shop&pr=1&path=%E0%A4%A",
    "?repo=acme/shop&pr=1&path=%2F%25E0%25A4%25A",
  ]) {
    refuses(search);
  }
});

test("the page writes no HTML from the query", () => {
  for (const sink of ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "Function("]) {
    assert.ok(!script.includes(sink), `the script uses ${sink}`);
  }
  assert.equal(scripts.length, 1, "one inline script, the one the CSP allows");
  assert.ok(!/<script[^>]+src=/.test(html), "no script from anywhere");
});

test("the CSP allows only the page's own script", () => {
  const hash = `'sha256-${createHash("sha256").update(script, "utf8").digest("base64")}'`;
  const meta = /<meta http-equiv="Content-Security-Policy" content="([^"]+)">/.exec(html)?.[1] ?? "";
  assert.ok(meta.includes(`script-src ${hash};`), `the page's CSP should allow ${hash} (update index.html and vercel.json)`);
  assert.ok(meta.startsWith("default-src 'none';"));
  const vercel = JSON.parse(readFileSync(new URL("../vercel.json", here), "utf8"));
  const review = vercel.headers.find((h) => h.source.startsWith("/review"));
  const csp = review.headers.find((h) => h.key === "Content-Security-Policy").value;
  assert.ok(csp.includes(`script-src ${hash};`), `vercel.json's CSP should allow ${hash}`);
  assert.ok(csp.includes("frame-ancestors 'none'"));
  assert.equal(review.headers.find((h) => h.key === "Referrer-Policy").value, "no-referrer");
  assert.ok(vercel.rewrites.some((r) => r.source === "/review" && r.destination === "/review/index.html"));
  assert.ok(html.includes('<meta name="referrer" content="no-referrer">'));
});

test("the badge is small, self-contained SVG", () => {
  const svg = readFileSync(new URL("../badges/review.svg", here), "utf8");
  assert.ok(svg.length < 3000, `${svg.length} bytes`);
  assert.match(svg, /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg" width="\d+" height="2[0-8]"/);
  assert.ok(svg.includes("Review in Shipyard"));
  // Nothing it loads or runs: no links, images, scripts, styles or fonts
  // from elsewhere.
  for (const bad of ["href", "<image", "<script", "<foreignObject", "@import", "url(http", "<style", "on"]) {
    const re = bad === "on" ? /\son[a-z]+=/i : new RegExp(bad.replace(/[()]/g, "\\$&"), "i");
    assert.ok(!re.test(svg), `the badge has ${bad}`);
  }
  assert.deepEqual([...svg.matchAll(/https?:\/\/[^"]+/g)].map((m) => m[0]), ["http://www.w3.org/2000/svg"]);
});
