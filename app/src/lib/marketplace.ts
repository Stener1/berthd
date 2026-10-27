// The plugin marketplace: plugins that aren't built into the app, listed on
// the public site and installed into ~/.berth/plugins by the laptop agent
// (GET /v1/marketplace, internal/agent/marketplace.go). Installing never
// turns a plugin on: the consent dialog does, as for any plugin of yours.

// MarketFile is one file of a listed plugin, with the size and checksum the
// agent holds the download to.
export interface MarketFile {
  path: string;
  url: string;
  sha256: string;
  size: number;
}

// MarketPlugin is a listed plugin with what this computer has of it.
export interface MarketPlugin {
  id: string;
  name: string;
  description?: string;
  version: string;
  author?: string;
  homepage?: string;
  files: MarketFile[];
  // All its files together, in bytes.
  size: number;
  // Why it can't be installed: the listing describes it wrongly.
  error?: string;
  // ~/.berth/plugins/<id> exists, at this version.
  installed?: boolean;
  installed_version?: string;
  // Installed from a listing, and this one has other files.
  update?: boolean;
  // The folder wasn't installed from a listing, or was changed since:
  // installing over it has to be asked for.
  by_hand?: boolean;
}

export interface Marketplace {
  // Where the listing was read from, and that address's host.
  registry: string;
  host: string;
  fetched: string;
  plugins: MarketPlugin[];
}

// What a listed plugin's row offers: "install" (not here yet), "update" (a
// newer listing than what was installed), "replace" (a folder of your own is
// in the way), "installed" (nothing to do) or "unavailable" (the listing is
// wrong about it).
export type MarketAction = "install" | "update" | "replace" | "installed" | "unavailable";

export function marketAction(p: MarketPlugin): MarketAction {
  if (p.error) return "unavailable";
  if (!p.installed) return "install";
  if (p.by_hand) return "replace";
  return p.update ? "update" : "installed";
}

// marketNote is the line under a listed plugin that says what installing
// would do to what is already there, when something is.
export function marketNote(p: MarketPlugin): string | undefined {
  switch (marketAction(p)) {
    case "update":
      return p.installed_version && p.installed_version !== p.version ? `You have ${p.installed_version}.` : "The listing has newer files for this version.";
    case "replace":
      return `~/.berth/plugins/${p.id} is your own${p.installed_version ? ` (${p.installed_version})` : ""}. Replacing it deletes what's there.`;
    default:
      return undefined;
  }
}
