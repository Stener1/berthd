---
name: release
description: Cut a Shipyard release (vX.Y.Z) - version bump, changelog, tag, and checking the published app and update feed. Use when asked to release, retag, or check a release.
---

# Releasing

The tag is the release. Pushing `vX.Y.Z` runs `.github/workflows/release.yml`:

1. **checks:** calls `ci.yml`: Go with `-race` and a real Chromium, shellcheck, the app's checks, tests and build, and the docs build. A tag skips the e2e suite. It runs alongside the builds; nothing is published if any check fails (the release stays a draft).
2. **tmux:** static tmux for Linux boxes (Docker, on a hosted runner).
3. **release:** builds `berth`/`berthd` archives for darwin and linux, amd64 and arm64, and makes a **draft** release.
4. **macos** and **linux:** the universal Mac app (signed, notarized, stapled) and the Linux alpha. They upload their bundles and `latest.json` to the draft.
5. **feed:** once the apps are built and the checks pass, merges the Linux entries into `latest.json` and publishes the release as Latest. About 15 minutes from the tag when the caches are warm.

The builds restore caches `release-cache.yml` keeps warm on `main` (a tag's run reads only `main`'s caches): the Mac's Go build cache and the Mac and Linux apps' Rust dependencies. It runs when Go or Rust dependencies change and twice a week; run it by hand (`gh workflow run release-cache.yml`) before a release if it hasn't run in a week.

Only people the "Release tags" ruleset lets through can push `v*` tags. An agent usually can't push a tag; prepare everything and give the user the command.

## Prepare

```sh
V=0.3.11   # no leading v
sed -i '' "s/\"version\": \"OLD\"/\"version\": \"$V\"/" app/package.json app/src-tauri/tauri.conf.json
sed -i '' "s/^version = \"OLD\"/version = \"$V\"/" app/src-tauri/Cargo.toml
(cd app/src-tauri && cargo update -p berth --offline)   # Cargo.lock
sed -i '' "s/^## Unreleased$/## v$V/" docs/changelog.mdx
git commit -am "release: v$V"
```

If the release has an entry in `app/src/lib/whats-new-releases.ts`, its version must match.

The release PR runs no CI of its own (no `run-ci` label needed): the tag's run checks everything before anything is published. Once it is merged, the user tags the merged commit:

```sh
git fetch origin && git tag -a "v$V" -m "Shipyard v$V" origin/main && git push origin "v$V"
```

## Retag after a failure

Nothing is published until the macos job finishes, so a failed run leaves at most a draft.

```sh
gh run cancel <run-id>                 # if still running
gh release delete "v$V" --yes || true     # only if a draft exists
git push origin :refs/tags/v$V && git tag -f -a "v$V" -m "Shipyard v$V" <sha> && git push origin "v$V"
```

## Check it's live

```sh
gh run watch <run-id> --exit-status
gh release list --limit 2                                   # v$V marked Latest
curl -fsSL https://github.com/cosscom/shipyard/releases/latest/download/latest.json | jq .version
gh release download "v$V" -p 'Berth-macos-universal.dmg' -D /tmp/rel   # asset names keep "Berth"
hdiutil attach -nobrowse -mountpoint /tmp/rel/mnt /tmp/rel/Berth-macos-universal.dmg
spctl -a -vv /tmp/rel/mnt/*.app     # expect: source=Notarized Developer ID
hdiutil detach /tmp/rel/mnt
```

Boxes keep their old `berthd` until upgraded (`berth upgrade BOX`, or "Update all" in the app's status bar). Say so in the release notes when a feature needs the new daemon.
