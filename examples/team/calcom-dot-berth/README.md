# calcom/.berth (example)

Cal.com's **team setup** for Berth, as it would live in the `calcom/.berth`
repository on GitHub: what a new engineer's box needs, set up in one go.
Berth finds it from the org's name (`calcom`), or from a link,
`berth://team?org=calcom`. Anyone who can read `calcom/.berth` and the
repositories it lists can run it; there is no separate membership check.
See [the Team setup guide](../../../docs/guides/team-setup.mdx).

```text
team.json                       the team setup (schema berth.team/v1)
box/setup.sh                    box-level setup, run once, by you, in a terminal on the box
box/init-cal.sh                 first-time setup of the main cal.com checkout
projects/private-api/kit.json   a kit kept here, for a repo that has no .berth/config.json
```

## What runs, and where your password goes

`box/setup.sh` runs in a terminal tab on your box, as you. Three steps
(system packages, Docker, gh and op) call `sudo`, which asks for your password
in that terminal. Berth never sees it and nothing keeps it. The script refuses
to run as root, and every step checks first, so it is safe to run again:
`box/setup.sh plan`, `box/setup.sh check postgres`, `box/setup.sh postgres`.

After the team's steps, Berth signs the box in to GitHub with its own
`gh auth login` (a device code, in the same terminal), so the box clones with
a credential of its own that you can revoke without touching your laptop's.

## Where each repo's setup comes from

1. The repo's own committed `.berth/config.json`, when it has one. Accepting
   the team setup trusts that file as you reviewed it; a different file asks
   again.
2. Otherwise the kit named for it here (`cal.com` uses
   [berth-kit-calcom](https://github.com/sean-brydon/berth-kit-calcom), pinned
   to a commit; `private-api` uses the kit in `projects/private-api`).
3. Then your own additions, in Project settings on your box.

Several apps in one repo (the web app, API v2, Prisma Studio) are that repo's
kit's services, not the team setup's.

## Keys

No secrets in git, ever. Shared keys are `op://` references, read on the box
with your own 1Password. Keys that are yours alone (`SENDGRID_API_KEY`) are
asked for once and kept on your box. Ports, URLs and database URLs are the
kits' job.

## Open questions (check before this is real)

- **Node version.** Cal.com has no `.nvmrc`; 20 is a guess from CI.
  `CAL_NODE` overrides it, and the step's title says "Node 20".
- **Which repo.** Whether engineers clone `calcom/cal.com` or the internal
  `calcom/cal`.
- **private-api.** Whether every engineer needs `calcom/private-api` (it is
  `required: false` here), and its setup: the kit here is a sketch.
- **1Password item names.** The vault and items (`Engineering/…`) are
  placeholders, as is the `contact` channel.
- `yarn workspace @calcom/prisma db-deploy` and `seed-basic` script names.
- macOS: Colima is assumed for Docker; OrbStack or Docker Desktop pass the
  check too.
