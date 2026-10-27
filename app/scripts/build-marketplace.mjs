// Builds the plugin listing people browse in Settings → Plugins: every
// plugins/<id>/ whose berth-plugin.json says "marketplace": true becomes
// site/plugins/<id>/<version>/ (its manifest, its main module, and any
// themes and kits the manifest names), and site/plugins/index.json lists
// them with each file's size and SHA-256. The laptop agent reads that index
// from the public site and installs from it (internal/agent/marketplace.go).
//
// site/plugins/ is committed, like site/demo/: publishing a plugin is running
// this and committing what it wrote. Bump the plugin's version when its code
// changes; the folder is rebuilt from nothing each time, so the listing is
// only ever what the sources build.
//
// The bundle is built as a plugin in ~/.berth/plugins is: React and the
// Shipyard SDK stay external, and it is not minified, so the code a person is
// asked to allow can be read.
//
// A plugin's helper ("box": {"helper": …}, a program it runs on a box) goes
// into the listed manifest as it is written, with the address, size and
// SHA-256 of its build for each platform ("assets"). Those checksums are
// what a person allows with the plugin, so they are only ever the ones of
// builds that were released:
//
//   - For a helper built in this repository (scripts/helpers.json is the
//     table of them: berth-leadline, berth-database), the assets are worked
//     out here and whatever the plugin's own manifest says of them is
//     dropped. --helper-bin DIR reads the four builds the helper's make
//     target writes (DIR/<name>-<os>-<arch>) and lists them at their
//     release's address; scripts/release-helper.sh does that, and uploads
//     those very files. Without it, the assets the listing already has for
//     that version of the helper are kept, so rebuilding the listing never
//     needs the builds; a version with no release yet is listed with no
//     assets, and installs nowhere until it has one.
//   - Any other helper's assets are the plugin's own to give, and are checked.
//
//   node scripts/build-marketplace.mjs           build site/plugins/
//   node scripts/build-marketplace.mjs --check   fail if site/plugins/ is not what the sources build
//
//   --helper-bin DIR   take the builds of this repository's helpers from DIR
//   --helper NAME      with --helper-bin: only that helper's; the others keep
//                      the builds they are listed with (releasing one helper
//                      does not need the others built)
//   --plugins DIR      read plugins from DIR instead of plugins/   (tests)
//   --out DIR          the listing's folder instead of site/plugins/ (tests)
import { build } from "esbuild";
import { createHash } from "node:crypto";
import { readFileSync, rmSync } from "node:fs";
import { cp, mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, posix, relative, resolve, sep } from "node:path";

// The listing is put together in a folder of its own and takes the place of
// the old one only when all of it built: a build that fails leaves the
// listing as it was. scratch is that folder, for fail to remove.
let scratch;

function option(name) {
  const i = process.argv.indexOf(name);
  if (i < 0) return undefined;
  const value = process.argv[i + 1];
  if (!value || value.startsWith("--")) fail(`${name} needs a folder`);
  return resolve(value);
}

// word is an option's value that is a name, not a folder.
function word(name) {
  const i = process.argv.indexOf(name);
  if (i < 0) return undefined;
  const value = process.argv[i + 1];
  if (!value || value.startsWith("--")) fail(`${name} needs a name`);
  return value;
}

const root = option("--plugins") ?? resolve(import.meta.dirname, "..", "..", "plugins");
const site = option("--out") ?? resolve(import.meta.dirname, "..", "..", "site", "plugins");
const helperBin = option("--helper-bin");
const onlyHelper = word("--helper");
// Packages a plugin bundles (not React or the SDK) come from the app's.
const nodePaths = [resolve(import.meta.dirname, "..", "node_modules")];
const check = process.argv.includes("--check");
const MANIFEST = "berth-plugin.json";

// The same limits the agent holds a listing to.
const ID = /^[a-z0-9][a-z0-9-]{0,47}$/;
const VERSION = /^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$/;
const MAX_FILE = 16 << 20;

// And the ones berthd holds a helper to (internal/box/helpers.go).
const HELPER_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const SHA256 = /^[0-9a-f]{64}$/;
const MAX_HELPER = 128 << 20;
const PLATFORMS = ["linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"];

// The helpers built in this repository, by the name of their program:
// scripts/helpers.json, which the release script reads too. Each version's
// builds are attached to a release named for the helper ("release") and
// that version.
const RELEASES = "https://github.com/cosscom/shipyard/releases/download";
const RELEASE_SCRIPT = "scripts/release-helper.sh";
const REPO_HELPERS = JSON.parse(readFileSync(resolve(import.meta.dirname, "..", "..", "scripts", "helpers.json"), "utf8"));

function fail(message) {
  console.error(`build-marketplace: ${message}`);
  if (scratch) rmSync(scratch, { recursive: true, force: true });
  process.exit(1);
}

for (const [name, h] of Object.entries(REPO_HELPERS)) {
  if (!HELPER_NAME.test(name) || !/^[a-z0-9][a-z0-9-]*$/.test(h.release ?? "")) fail(`scripts/helpers.json: the row of ${name} needs a "release" of lowercase letters, digits and dashes`);
}
if (onlyHelper && !REPO_HELPERS[onlyHelper]) fail(`--helper ${onlyHelper} is not a helper of scripts/helpers.json (${Object.keys(REPO_HELPERS).join(", ")})`);
if (onlyHelper && !helperBin) fail("--helper goes with --helper-bin");

// inside is a path the manifest names, kept inside the plugin's folder.
function inside(id, rel) {
  const clean = posix.normalize(String(rel));
  if (!clean || clean.startsWith("..") || posix.isAbsolute(clean) || clean.includes("\\")) fail(`${id}: ${rel} is not a path inside the plugin's folder`);
  return clean.replace(/\/$/, "");
}

async function walk(dir, base = dir) {
  const out = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...(await walk(p, base)));
    else if (e.isFile()) out.push(relative(base, p).split(sep).join("/"));
  }
  return out.sort();
}

// checkAssets says what is wrong with a helper's assets, or nothing.
function checkAssets(assets) {
  if (!assets || typeof assets !== "object" || Array.isArray(assets)) return "its assets must be an object, by platform";
  for (const [platform, a] of Object.entries(assets)) {
    if (!PLATFORMS.includes(platform)) return `${platform} is not a platform (${PLATFORMS.join(", ")})`;
    let url;
    try {
      url = new URL(a?.url);
    } catch {
      return `its ${platform} build has no address`;
    }
    if (url.protocol !== "https:" || url.username || url.password) return `its ${platform} build must be at a plain https:// address`;
    if (!SHA256.test(a.sha256 ?? "")) return `its ${platform} build has no SHA-256 checksum`;
    if (!Number.isInteger(a.size) || a.size <= 0 || a.size > MAX_HELPER) return `its ${platform} build's size must be between 1 byte and ${MAX_HELPER >> 20} MB`;
  }
  return "";
}

// listedHelpers is the helper each plugin has in the listing as it stands,
// read before the listing is built again.
async function listedHelpers(dir) {
  const out = new Map();
  let index;
  try {
    index = JSON.parse(await readFile(join(dir, "index.json"), "utf8"));
  } catch {
    return out;
  }
  for (const p of index.plugins ?? []) {
    try {
      const m = JSON.parse(await readFile(join(dir, p.id, p.version, MANIFEST), "utf8"));
      if (m.box?.helper) out.set(p.id, m.box.helper);
    } catch {
      // A listing that names a manifest it does not have keeps nothing.
    }
  }
  return out;
}

// helperOf is a plugin's helper as the listing carries it: the manifest's
// own words, with the assets worked out for one built in this repository.
async function helperOf(id, helper, listed) {
  if (!helper || typeof helper !== "object") fail(`${id}: box.helper must be an object`);
  if (!HELPER_NAME.test(helper.name ?? "")) fail(`${id}: its helper's name ${JSON.stringify(helper.name)} must be lowercase letters, digits, dots and dashes`);
  if (!VERSION.test(helper.version ?? "")) fail(`${id}: its helper needs a version`);
  const repo = REPO_HELPERS[helper.name];
  if (!repo) {
    const problem = checkAssets(helper.assets);
    if (problem) fail(`${id}: its helper ${helper.name}: ${problem}`);
    return helper;
  }
  // The builds this version of it is listed with now, if it has any.
  const same = listed?.name === helper.name && listed?.version === helper.version;
  const before = same && !checkAssets(listed.assets) && Object.keys(listed.assets).length > 0 ? listed.assets : undefined;
  const script = `${RELEASE_SCRIPT} ${helper.name}`;
  let assets = {};
  if (helperBin && (!onlyHelper || onlyHelper === helper.name)) {
    for (const platform of PLATFORMS) {
      const file = join(helperBin, `${helper.name}-${platform}`);
      let data;
      try {
        data = await readFile(file);
      } catch {
        fail(`${id}: ${file} is not there. Build the helper for every platform first (${script} does).`);
      }
      if (data.length === 0 || data.length > MAX_HELPER) fail(`${id}: ${file} must be between 1 byte and ${MAX_HELPER >> 20} MB`);
      assets[platform] = {
        url: `${RELEASES}/${repo.release}-v${helper.version}/${helper.name}-${platform}`,
        sha256: createHash("sha256").update(data).digest("hex"),
        size: data.length,
      };
    }
    // A release's files do not change: what people allowed names them by
    // checksum. Other builds are another version.
    if (before && JSON.stringify(before) !== JSON.stringify(assets)) {
      fail(`${id}: ${helper.name} ${helper.version} is already listed with other checksums. Bump box.helper.version to release new builds.`);
    }
  } else if (before) {
    assets = before;
  } else if (!check) {
    console.error(`build-marketplace: ${id}: ${helper.name} ${helper.version} has no released builds yet, so it is listed with none and installs on no box. Release them with ${script}.`);
  }
  return { ...helper, assets };
}

async function buildAll(out) {
  // What the listing has now, from the folder it is kept in.
  const listed = await listedHelpers(site);
  await mkdir(out, { recursive: true });
  const plugins = [];
  const dirs = (await readdir(root, { withFileTypes: true })).filter((d) => d.isDirectory()).sort((a, b) => a.name.localeCompare(b.name));
  for (const dir of dirs) {
    let m;
    try {
      m = JSON.parse(await readFile(join(root, dir.name, MANIFEST), "utf8"));
    } catch {
      continue;
    }
    if (!m.marketplace) continue;
    const id = m.id ?? dir.name;
    if (!ID.test(id)) fail(`${dir.name}: the id ${JSON.stringify(id)} must be lowercase letters, digits and dashes`);
    if (m.builtin) fail(`${id}: a plugin is built in or in the marketplace, not both`);
    if (!VERSION.test(m.version ?? "")) fail(`${id}: its ${MANIFEST} needs a version`);
    if (plugins.some((p) => p.id === id)) fail(`${id}: listed twice`);
    const main = inside(id, m.main ?? "dist/index.js");
    const target = join(out, id, m.version);
    await mkdir(target, { recursive: true });

    await build({
      // From the plugin's own folder, so the bundle is the same wherever
      // this runs from.
      absWorkingDir: join(root, dir.name),
      entryPoints: ["src/index.tsx"],
      bundle: true,
      format: "esm",
      jsx: "automatic",
      // A plugin can carry a script it runs on boxes as text.
      loader: { ".py": "text", ".sh": "text" },
      target: "es2022",
      external: ["react", "react/jsx-runtime", "react-dom", "@berth/plugin", "@berth/plugin/ui"],
      outfile: join(target, main),
      nodePaths,
      logLevel: "warning",
    });
    for (const rel of [...(m.themes ?? []), ...(m.kits ?? [])]) {
      const clean = inside(id, rel);
      await mkdir(dirname(join(target, clean)), { recursive: true });
      await cp(join(root, dir.name, clean), join(target, clean), { recursive: true });
    }
    // The manifest as it is installed: "marketplace" is about this
    // repository, not about the plugin.
    const { marketplace, ...manifest } = { ...m, id, main };
    if (m.box?.helper !== undefined) manifest.box = { ...m.box, helper: await helperOf(id, m.box.helper, listed.get(id)) };
    await writeFile(join(target, MANIFEST), JSON.stringify(manifest, null, 2) + "\n");

    const files = [];
    for (const path of await walk(target)) {
      const data = await readFile(join(target, path));
      if (data.length > MAX_FILE) fail(`${id}: ${path} is larger than ${MAX_FILE >> 20} MB`);
      files.push({ path, url: `${id}/${m.version}/${path}`, sha256: createHash("sha256").update(data).digest("hex"), size: data.length });
    }
    plugins.push({ id, name: m.name ?? id, description: m.description, version: m.version, author: m.author, homepage: m.homepage, files });
  }
  await writeFile(join(out, "index.json"), JSON.stringify({ plugins }, null, 2) + "\n");
  return plugins;
}

scratch = await mkdtemp(join(tmpdir(), "berth-marketplace-"));
try {
  const plugins = await buildAll(scratch);
  if (check) {
    const [want, have] = await Promise.all([walk(scratch), walk(site).catch(() => [])]);
    const stale = new Set([...want.filter((p) => !have.includes(p)), ...have.filter((p) => !want.includes(p))]);
    for (const p of want.filter((p) => have.includes(p))) {
      if (!(await readFile(join(scratch, p))).equals(await readFile(join(site, p)))) stale.add(p);
    }
    if (stale.size) fail(`site/plugins/ is not what plugins/ builds (${[...stale].sort().join(", ")}). Run pnpm -C app build:marketplace and commit it.`);
    console.log("marketplace: site/plugins/ is up to date");
  } else {
    await rm(site, { recursive: true, force: true });
    await cp(scratch, site, { recursive: true });
    console.log(`marketplace: ${plugins.map((p) => `${p.id} ${p.version}`).join(", ") || "none"}`);
  }
} finally {
  await rm(scratch, { recursive: true, force: true });
}
