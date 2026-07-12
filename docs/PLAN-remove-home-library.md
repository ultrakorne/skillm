# Plan: remove the Home library — installs are the only copies

Self-contained execution plan. Written 2026-07-12 on branch `skill-compatible`, right
after commit `8f61db2` (global installs are vercel-shaped) and `1bda708`. A fresh
context can execute this top to bottom; all design decisions below are already agreed
with the user — do not re-litigate them.

## Target model (agreed, final)

- `~/.skillm` holds **only** `config.toml` and `state.toml`. The skills library
  (`~/.skillm/skills/<id>`) is removed entirely. No agent ever read it; now nothing does.
- The canonical stores are the **only** copies of a skill's files:
  - Global install → `~/.agents/skills/<id>` (+ absolute symlinks in other agents'
    user-level folders, e.g. `~/.claude/skills/<id>`).
  - Local install → `<project>/.agents/skills/<id>` (+ relative in-repo symlinks,
    `skills-lock.json` entry). Unchanged from today.
  - A skill installed only locally never appears in `~/.agents/skills`. Installed
    globally means globally active — that is the accepted semantic.
- **`add` is removed.** `install` is the single entry point and must resolve a scope
  up front (flag or interactive scope picker): `install <url|path> [ids...] [--all]
  [--as] [--ref] --global|--local` fetches and writes straight into the chosen scope.
- `state.toml` remains skillm's differentiator vs vercel: per-skill Source/Path/Ref/
  Revision + the machine-wide install index (`global` flag, `vendored_at` roots,
  `local_roots`). **An entry exists iff the skill is installed somewhere**; removing
  the last install drops the entry.
- Single Revision per entry (not per install). All installs of a skill sync to the
  same upstream revision on `update`; drift between installs can only exist between
  updates and is not modeled.
- Local-path skills (`kind = local`): the recorded **source directory is the
  upstream**. `update` re-syncs installs from that path when content differs
  (`store.DirContentEqual`); if the path no longer exists, warn once and leave the
  installs standalone (do not prune them).
- Per the user's standing no-migration-code rule: no auto-migration. Rerunning
  `skillm install` at the affected scope converts old layouts in place (the linker's
  recognition of legacy symlinks into `~/.skillm/skills` — `linkIntoHome`/`ownedLink`
  and `Classify`'s `TargetOurLink` — must therefore be **kept** for now; it is what
  makes self-heal and uninstall-of-legacy work).

## How each operation works afterwards

- **install from a source**: treeless clone / local copy into a **staging temp dir**
  (existing `gitx.MaterializeSubdir` path), pick skills (id args / `--all` /
  interactive), then for each: `vendorOne`-style write from the staged dir into the
  scope's canonical slot, link agents, upsert registry entry (Source/Path/Ref/Revision,
  `global=true` or `vendored_at+=root`), lock entry at local scope. Same-id different-
  source is still an atomic collision error resolved with `--as`; same source reuses
  nothing anymore (there is no Home copy) — it just installs the fetched content.
- **install by id** (already-registered skill, adding another scope/project):
  copy source of truth in this order: (1) the canonical global copy if `entry.Global`
  and the dir exists; (2) otherwise re-fetch from `entry.Source@entry.Ref` (staged),
  pinned to... current upstream of the ref — record the new Revision if it advanced
  (document this in the command help; re-fetch may bump the revision, that is fine).
- **update**: per git skill, one treeless clone, compare `SubtreeSHA` vs
  `entry.Revision`; when changed, materialize staged, then `store.ReplaceDir` the
  staged content into **every recorded install** (global slot + each vendored root),
  refresh lock entries (local roots only), re-link **enabled** agents
  (`linkVendorAgents`), update `entry.Revision`. No Home write. Local-kind skills:
  sync installs from the source dir when it exists and differs. Prune semantics
  unchanged (vanished copy → forget that install; last install gone → drop entry? NO —
  update only prunes installs; dropping entries is uninstall's job, but if pruning
  removes the last install, drop the entry too, matching "entry iff installed").
- **uninstall**: unchanged flow minus the "delete the Home copy" step
  (`store.RemoveSkillDir` call goes away); it already deletes the global copy, local
  copies, links, lock entries, and the registry entry.
- **check**: unchanged (compares upstream SHA vs `entry.Revision`; no disk reads of
  a library).
- **import**: unchanged conceptually; where it restored a missing canonical copy
  "from Home", it now fetches the locked source into a staging dir and writes the
  copy from there (it already clones per source group — reuse that clone).
- **agent enable/disable, list, installedMark**: already operate on canonical copies
  and live link scans; only references to `store.SkillDir`/`store.Exists` as a
  content source need replacing (see inventory below).

## Work inventory (file by file)

1. **`internal/store`** — shrink. Keep: `Home()` resolution (`--home`,
   `$SKILLM_HOME`, default `~/.skillm`), `EnsureHome` (now just MkdirAll of the home
   dir — no `skills/` subdir), `ReplaceDir`, `DirContentEqual`, and the copy helpers
   `AddSkillDir` is renamed/refactored to a generic "copy dir with staging"
   (`CopyDir(src, dst)`) used by install/import. Delete: `SkillsDir`, `SkillDir`,
   `Exists`, `RemoveSkillDir` (and their tests). Grep for every caller first:
   `grep -rn "store\.\(SkillDir\|SkillsDir\|Exists\|AddSkillDir\|RemoveSkillDir\)" cmd internal`.
2. **`cmd/add.go`** — delete (and its tests). README/help updated accordingly.
3. **`cmd/fetch.go`** — this is the shared fetch pipeline (`fetchToHome`, used by
   add + install source mode). Rework into `fetchToStage`: same source
   classification, ref pinning, catalog discovery, interactive picking, `--as`
   validation, collision checks against the **registry** (not Home dirs) — but
   materialize into a caller-owned temp dir and return `[]{entry, stagedDir}`.
   Registry writes move to the install layer (entry is only recorded once an install
   actually lands).
4. **`cmd/install.go`** — source mode calls `fetchToStage` then `installVendored`
   with staged dirs as the copy source. Id mode resolves ids against the registry
   (`validateInHome` → `validateRegistered`), copy source = global canonical or
   re-fetch (see above). The **no-arg picker changes meaning**: with no source and no
   ids, pick from *registered* skills (useful for adding a scope) — keep it, but it
   now lists registry entries annotated by `installedMark`. `installVendored` gains a
   `srcDir(id) string` resolver instead of assuming `store.SkillDir(home, id)`.
5. **`cmd/localinstall.go`** (`vendorOne` & co) — parameterize the copy source
   (currently `store.SkillDir(home, id)`); everything else stands.
6. **`cmd/update.go`** — `updateOne` returns the staged dir instead of writing Home;
   `refreshVendoredCopies` takes the staged/source dir per skill instead of reading
   Home; local-kind sync source = `entry.Source` path. Entry dropped when its last
   install is pruned.
7. **`cmd/uninstall.go`** — remove the `store.RemoveSkillDir` step; `selectUninstallIDs`
   validates against the registry only.
8. **`cmd/import.go`** — replace "restore copy from Home" with "write copy from the
   group clone's materialized subdir".
9. **`cmd/list.go` / `cmd/check.go` / `cmd/agent.go`** — replace any
   `store.Exists`/`store.SkillDir` reads (grep). `linkedLabel`/`servedAgents` logic
   unchanged.
10. **`internal/linker`** — no behavioral change. Keep `linkIntoHome` legacy
    recognition. Update package doc ("Home's skills/ subtree" is now purely the
    legacy shape).
11. **Docs** — README (drop `add` from quickstart/commands; new one-liner model),
    `docs/CONTEXT.md` (Home becomes "config + registry only"; Add verb removed;
    Install/Update/Import/Local-skill entries reworded), `docs/INDEX.md`,
    `docs/vercel-skills-comparison.md` (append: rec #2's "considered and rejected"
    reversed on 2026-07-12 by explicit user decision — installed-globally == active
    is accepted; note what was given up: fetch-without-activating).
12. **Tests** — delete `cmd/add_test.go`, store skills-dir tests; rework
    `cmd/fetch_test.go`, `integration_test.go` (no `add` steps — use
    `install <url> <id> --global/--local`; `assertGlobalInstalled` etc. stand),
    `install_source_test.go`, `localinstall_test.go` fixtures (`localTestSetup`
    seeds via a staged src dir instead of `store.AddSkillDir`). New coverage:
    install-by-id copies from global canonical; install-by-id re-fetches when only
    local installs exist; update writes straight to all installs; local-kind skill
    syncs from source dir; entry dropped with last install.

Suggested commit slicing: (1) fetch pipeline → staging + install rewiring,
(2) update/import/uninstall off Home, (3) delete add + store shrink, (4) docs.
Run `go build ./... && go test ./...` between slices; `gofmt -l .` before each commit.

## This machine's migration (run after the build, manual by design)

State as of writing: 10 registry entries; 6 global (real copies already in
`~/.agents/skills`, `global = true` recorded); `impeccable`/`unity-cli` vendored in
projects; `teach`/`last30days` have **legacy local symlinks into `~/.skillm/skills`**
at `/Users/ultrakorne/Documents/starberry` (not converted, no `vendored_at`).

1. Rerun `skillm install teach last30days --local` from `~/Documents/starberry`
   (converts the legacy links to project copies) — do this **before** deleting the
   library or those links dangle.
2. Verify `skillm list` shows every entry served by copies, then
   `rm -rf ~/.skillm/skills`.
3. `skillm check` and `skillm update` as a smoke test; rebuild → `cp` binary to
   `~/.local/bin/skillm`.

## Acceptance checks

- `~/.skillm` contains only the two toml files on a fresh install.
- `install <url> <id> --global` → copy in `~/.agents/skills/<id>`, claude link,
  `global = true`, no other copy anywhere.
- `install <id> --local` in a project with a global install present → copy taken
  from the global canonical (no network); with only-local installs → re-fetch.
- `update` with one outdated skill installed at 3 places rewrites all 3 from one
  clone and bumps one Revision.
- Uninstalling a skill's last install removes its registry entry.
- `add` prints "unknown command".
