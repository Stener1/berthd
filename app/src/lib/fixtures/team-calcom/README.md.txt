# calcom/.berth (draft)

Cal.com's **team setup** for Berth: what a new engineer's box needs, set up in
one go. Berth finds it from the org's name (`calcom`), or from a link:
`berth://team?org=calcom`. Only members of the `calcom` GitHub org can run it.

```text
team.json                       the team setup (schema berth.team/v1, a draft)
box/setup.sh                    box-level setup, run once, by you, in a terminal on the box
box/init-cal.sh                 first-time setup of the main cal.com checkout
projects/private-api/kit.json   a kit kept here, for a repo that has no .berth/config.json
```

## What runs, and where your password goes

`box/setup.sh` runs in a visible terminal tab on your box, as you. Three steps
(system packages, Docker, gh and op) call `sudo`, which asks for your password
in that terminal. Berth never sees it and nothing keeps it. The script refuses
to run as root, and every step checks first, so it is safe to run again:
`box/setup.sh check postgres`, `box/setup.sh postgres`, `box/setup.sh plan`.

## Where each repo's setup comes from

1. The repo's own committed `.berth/config.json`, when it has one.
2. Otherwise the kit named for it here (`cal.com` uses
   [berth-kit-calcom](https://github.com/sean-brydon/berth-kit-calcom), pinned to a commit).
3. Then your own additions, in Project settings on your box.

## Keys

No secrets in git, ever. Shared keys are `op://` references, read on the box with
your own 1Password. Keys that are yours alone (`SENDGRID_API_KEY`) are asked
for once and kept on your box. Ports, URLs and database URLs are the kits' job.

## UNSURE (to check before this is real)

- Node version: Cal.com has no `.nvmrc`; 20 is a guess from CI. `CAL_NODE` overrides it.
- `yarn workspace @calcom/prisma db-deploy` and `seed-basic` script names.
- Whether every engineer needs `calcom/private-api`, and its setup (the kit here is a sketch).
- macOS: Colima is assumed for Docker; OrbStack or Docker Desktop pass the check too.
- 1Password vault and item names (`Engineering/…`) are placeholders.
- `contact` (#eng-onboarding) is a placeholder.
- Whether `calcom/cal.com` is the repo engineers clone, or the internal `calcom/cal`.
