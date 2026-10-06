# Cal.com team setup for Berth (example)

An example of a [team setup](../../../docs/guides/team-setup.mdx), made from a
real run of Cal.com's. **Cal.com's own setup lives privately**; this one
uses the public [`calcom/cal.com`](https://github.com/calcom/cal.com), and
is a copy of the `team/` folder of
[`sean-brydon/berth-kit-calcom`](https://github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team)'s
`team-setup` branch.

It sets a new engineer's box up the way the team's boxes are, in one
go: the box's system software, Docker with Postgres and Redis, Node and
Yarn, then `calcom/cal.com` cloned, migrated and seeded, with the
[cal-com kit](https://github.com/sean-brydon/berth-kit-calcom) giving every worktree its own database, `.env`
and ports.

Berth reads an org's team setup from `<org>/.berth/team.json`, so an org
publishes one like this by copying the folder's contents to the root of its
`.berth` repository. Every script here also runs by hand.

```text
team.json                       the team setup (schema berth.team/v1)
box/setup.sh                    the box steps, one subcommand each; run by you, in a terminal on the box
box/init-cal.sh                 the main cal.com checkout's one-time setup
```

## What it sets up

**On the box, once** (`box/setup.sh`). Steps marked sudo ask for your password.

| Step | | |
| --- | --- | --- |
| `update` | sudo | `apt-get update` and `apt-get upgrade`; counts as done for 24 hours |
| `packages` | sudo | git, curl, jq, unzip, build tools, python3, openssl, and `psql`/`pg_dump` of the Postgres version, from the PostgreSQL apt repository (the kit's worktree copies need a `pg_dump` at least as new as the server) |
| `docker` | sudo | Docker Engine from Docker's apt repository, started at boot, and you in the `docker` group |
| `cli` | sudo | `gh` and the 1Password CLI `op`, from their own apt repositories |
| `node` | | fnm in `~/.local/share/fnm`, and the Node version cal.com names, on the PATH of login shells |
| `yarn` | | `corepack enable`; cal.com's `packageManager` picks the Yarn version |
| `postgres` | | `cal-postgres`, Postgres 18, on `127.0.0.1:5450`, database `calendso`, data in the `cal-postgres-data` volume |
| `redis` | | `cal-redis`, Redis 8, on `127.0.0.1:6379`, data in the `cal-redis-data` volume |

**Versions** live in one place, `box.settings` in `team.json`
(`CAL_POSTGRES_VERSION`, `CAL_REDIS_VERSION`), so a bump is a one-line
change. Berth passes them to `box/setup.sh` as `BERTH_SETTING_CAL_POSTGRES_VERSION`
and `BERTH_SETTING_CAL_REDIS_VERSION`; run by hand, it reads them from
`team.json`, and the environment can override them. They match cal.com: Postgres 18
is what its CI services (`.github/workflows/*.yml`) and its own local compose
(`packages/prisma/docker-compose.yml`) run (19 is still in beta); Redis 8 is
what its `redis:latest` (CI and `apps/api/v2/docker-compose.yaml`) is today,
pinned to the major. After a bump, Redis moves to the new image with its data;
Postgres does not, since a new major cannot read the old one's data: the step
says so and how to move it, and leaves the old container running.

Both containers restart unless you stop them, so they come back after a
reboot, and listen on localhost only. Postgres takes the user `postgres`
with no password, as cal.com's `.env.example` expects.

Then Berth's own steps. **GitHub on the box**: `gh auth login` on the box,
with a device code you enter on your laptop. The box clones with a
credential of its own, which you can revoke on its own; your laptop's token
never goes to the box. **1Password on the box**, since the shared keys are
`op://` references: op signs in, in the box's terminal, so Berth can read
those keys when a worktree needs them, without ever asking in a service's
terminal.

**The docker group.** The `docker` step adds you to the `docker` group. A
process only gets a new group at a new login, so Berth starts what comes
after it (the remaining steps, `init-cal.sh`, terminals, services) with the
group, through `sg`; nothing needs restarting.

**The node version** is read when the step runs, not written here: cal.com's
`.nvmrc` or `.node-version`, else `engines.node` in its `package.json`, else
the Node version its CI uses (`.github/actions/yarn-install`, `v20.x` today),
else 20. `box/setup.sh node-version` prints which, and from where.
`init-cal.sh` reads it again from the checkout itself.

**The repos.** `calcom/cal.com` is cloned to `~/code/cal.com`, then:

1. Its setup comes from its own committed `.berth/config.json` when it has
   one; otherwise from the cal-com kit (`sean-brydon/berth-kit-calcom`), pinned to a
   commit in `team.json`; then whatever you add in Project settings on your
   box.
2. `box/init-cal.sh` runs once in the main checkout, which the kit leaves
   alone (it sets up worktrees): Node through fnm; `.env` from `.env.example`
   with `DATABASE_URL` and `DATABASE_DIRECT_URL` at the Docker Postgres and
   `REDIS_URL` at the Docker Redis, and a `NEXTAUTH_SECRET` and
   `CALENDSO_ENCRYPTION_KEY` made on the box; `.env.appStore` and
   `apps/api/v2/.env` from their examples; `yarn install` (whose postinstall
   runs `prisma generate`); `prisma migrate deploy`; and Cal.com's
   `seed-basic`. A value you changed in `.env` is kept.
3. Every worktree after that is the kit's: a copy of this database, its own
   `.env`, ports and URL, and dependencies from the shared yarn cache.

## Using it

### With Berth

Before it is published, try it straight from this branch: where Berth asks
for an org (**Add a box → Set this box up for a team**), paste

```text
github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team
```

or run `berth team show github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team`
and then `berth team setup <the same link> <box>`. Once it is in
`calcom/.berth`, type `calcom`, or open `berth://team?org=calcom`.

The page lists every step and command before anything runs. Steps run in a
terminal on the box that you can see; type your password there when sudo
asks.

### By hand

On the box, as yourself (not root):

```sh
git clone https://github.com/sean-brydon/berth-kit-calcom -b team-setup ~/berth-kit-calcom
~/berth-kit-calcom/team/box/setup.sh plan        # what it will do
~/berth-kit-calcom/team/box/setup.sh all         # every step, then gh auth login
gh repo clone calcom/cal.com ~/code/cal.com
cd ~/code/cal.com && ~/berth-kit-calcom/team/box/init-cal.sh
```

Other subcommands:

```sh
box/setup.sh check postgres    # exit 0 when that step has nothing left to do
box/setup.sh docker            # one step
box/setup.sh github            # just the GitHub sign-in
box/setup.sh node-version      # the Node version cal.com asks for, and where that came from
```

Settings, all optional: `CAL_PG_PORT` (5450), `CAL_REDIS_PORT` (6379),
`CAL_REPO` (`calcom/cal.com`), `CAL_DIR` (`~/code/cal.com`), `CAL_NODE`
(a Node version, over what cal.com names), `CAL_SKIP_GITHUB=1`,
`CAL_UPDATE_EVERY_HOURS` (24), and for `init-cal.sh`, `CAL_SKIP_SEED=1`.

## Safe to run again

Every step checks first and skips what is done, so running it again changes
nothing. Running `all` twice, or Berth's **Retry from**, starts where it got
to.

## Your password and your keys

- **sudo.** The four sudo steps call `sudo` themselves, in the terminal in
  front of you. The first asks for your password once, with a plain message
  saying why; sudo remembers it in that terminal for a few minutes, so the
  others don't ask again. Nothing stores it, Berth never sees it, and the
  scripts refuse to run as root.
- **No secrets in git.** `team.json` lists keys, never values:
  - *shared* keys (Stripe test keys, Daily, Google, Zoom, Microsoft) are
    1Password references, `op://vault/item/field`, read on the box with your
    own 1Password when a worktree needs them;
  - *asked* keys (`SENDGRID_API_KEY`, `SENDGRID_EMAIL`) are yours alone:
    Berth asks once and keeps them on your box, in the project's own config.
- **Env stays light.** Berth sets ports, URLs and databases; every other
  value stays as cal.com's `.env.example` has it.

## Updates

When this changes in `calcom/.berth`, Berth shows **Cal.com's setup changed ·
Review update** with what changed. It never runs by itself; reviewing and
pressing **Update** runs it, and steps whose checks pass are skipped.

## Supported systems

- **Ubuntu 22.04 and 24.04, Debian 12**: supported; Ubuntu 24.04 is tested
  from a fresh machine.
- **macOS 14+**: best effort. Homebrew installs the packages, gh, op and fnm;
  Docker is whatever already answers (OrbStack, Docker Desktop), else
  OrbStack if installed, else Colima. `update` runs `brew update` only.

## Placeholders

- **Repositories.** This example clones the public `calcom/cal.com` only.
  Cal.com's own setup, kept privately, decides which repositories its
  engineers get.
- **The project's id** is `cal`, not `cal.com`: it names the project on the
  box and is part of every worktree's URL (`<worktree>.cal.<box>.localhost`),
  so Berth refuses an id with a dot. `path` keeps the folder `~/code/cal.com`.
- **1Password names.** The vault (`dev`) and item names in `keys` are
  placeholders, as is the `contact` channel.
- **Which keys are shared and which are each engineer's own.** The split in
  `keys` is a first guess from `.env.example` and `.env.appStore.example`.
