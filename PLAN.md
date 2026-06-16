# stash-stash

> A concierge for your git stash graveyard.

## 1. Pitch

`git stash` is where good intentions go to die. You stash "real quick," switch
branches, and three weeks later you've got `stash@{7}` and zero memory of what's
in any of them. **stash-stash** is a tiny TUI that turns that anonymous LIFO pile
into a labeled, browsable, age-aware library — with a librarian who gently nags
you to either *revive* or *bury* the stashes rotting at the bottom.

## 2. Trend inspiration

- **The "terminal renaissance" of 2026** — focused, single-purpose TUIs are
  having a moment again. (1337skills.com, 2026-03: "the most exciting new tools
  are being built for the terminal"; `awesome-tuis` keeps growing.) People want
  small sharp TUIs, not another Electron app.
- **The eternal git-stash pain** — searching surfaces an endless stream of "Git
  Stash: Save Your Work Without the Panic" tutorials (dev.to 2025-12,
  oneuptime 2026-01, git-scm docs). The recurring theme: *people lose track of
  stashes and fear losing work.* The advice is always "be disciplined" — nobody
  ships a tool that makes the discipline automatic.
  - https://dev.to/vidya_kokkada/git-stash-save-your-work-without-the-panic-2emk
  - https://oneuptime.com/blog/post/2026-01-24-git-stash-effectively/view
  - https://stackoverflow.com/questions/12147042/lost-git-stash-changes
- **lazygit's stash panel** proves people *want* to manage stashes visually —
  but it's one panel inside a giant git client. There's room for a focused tool.

## 3. Why it's different

- **vs. `git stash list`** — that gives you `stash@{2}: WIP on main: a1b2c3d`.
  Useless. We give you a real label, an age ("23 days"), the branch it was born
  on, a file/line diffstat, and a freshness color. Skimmable in 2 seconds.
- **vs. lazygit / gitui / magit** — those are full git clients; stash is a tiny
  afterthought panel. stash-stash does *one* thing and makes it delightful:
  the stash lifecycle (label → browse → revive/bury → never-lose-work).
- **vs. `.env` drift tools (env-rx, wizard-of-drift, dotenvx)** — different
  problem entirely; mentioned only because that was the *crowded* lane we
  deliberately avoided. The stash-management lane is wide open.
- **The hook nobody else has:** *named stashes that survive.* Git can't natively
  name a stash well, so we keep a sidecar metadata file (`.git/stash-stash.json`)
  mapping stash commit SHAs → human labels, tags, and notes. Re-stashing or
  popping keeps the metadata coherent. Plus an **age nag**: run `stash-stash` and
  anything older than your threshold gets flagged for triage.

## 4. MVP scope (v0.1)

The smallest useful thing:

- `stash-stash` (no args) → opens a TUI list of all stashes with: index, label
  (or auto-derived), age, origin branch, diffstat (`+N -M`, files touched).
- Arrow keys to move; `Enter` to preview the full diff in a pager pane.
- `a` to apply, `p` to pop, `d` to drop (with a confirm), `l` to (re)label.
- Stashes older than `--stale-days` (default 14) are highlighted + a header
  banner: "3 stashes are gathering dust — triage them?"
- `stash-stash push -m "label"` → a thin wrapper around `git stash push` that
  records the label in the sidecar metadata immediately.
- Sidecar metadata stored in `.git/stash-stash.json`, keyed by stash *content*
  SHA so labels stick even as `stash@{n}` indices shuffle.
- Read-only-safe: never destroys a stash without an explicit keypress + confirm.

## 5. Tech stack

Boring, fast, batteries-included:

- **Go** — single static binary, trivial `go install`, fast startup, great for
  shelling out to `git`. TUI tools in Go age well and have no runtime deps.
- **Bubble Tea + Lip Gloss** (charmbracelet) — the de-facto Go TUI stack in 2026;
  mature, well-documented, handles the list/preview/keybinding layout cleanly.
- **`os/exec` over plain `git`** — no libgit2/CGo. We parse `git stash list`,
  `git stash show`, `git log` output. Keeps the binary pure-Go and portable.
- **`encoding/json`** stdlib for the sidecar metadata. No DB, no config server.

Justification: zero-install distribution, no Node/Python runtime to manage, and
shelling out to the user's real `git` means we inherit their config/credentials
for free and never reimplement git semantics.

## 6. Architecture

```
cmd/stash-stash/main.go     # entrypoint, flag parsing, subcommand dispatch
internal/git/               # thin wrappers: List(), Show(), Apply(), Pop(), Drop(), Push()
internal/meta/              # sidecar .git/stash-stash.json load/save, SHA<->label map
internal/model/             # Stash struct: index, sha, label, branch, age, diffstat
internal/tui/               # Bubble Tea model: list view, preview pane, keymap, nag banner
internal/age/               # staleness classification + color buckets
```

Key flows:
- **Boot:** `git stash list --format=...` → enrich each entry with metadata from
  the sidecar (matched by SHA) → compute age + diffstat → render list.
- **Mutations** (apply/pop/drop/label) go through `internal/git` + `internal/meta`
  together so the sidecar never drifts from reality.
- **Push wrapper:** record label *before* the stash exists is impossible, so we
  run `git stash push`, read back the new stash SHA, then write the label.

## 7. Milestones

1. **M1 — scaffold + hello-world.** Go module, `cmd/stash-stash` prints version &
   a stubbed "no stashes found" message. CI (build + `go vet`) green. README runs.
2. **M2 — read & list stashes.** `internal/git.List()` parses `git stash list`;
   render a plain (non-interactive) table: index, raw subject, age, branch.
3. **M3 — Bubble Tea TUI.** Interactive list with cursor nav + a preview pane
   showing `git stash show -p` for the selected stash. Quit with `q`.
4. **M4 — sidecar metadata + labels.** `.git/stash-stash.json` load/save keyed by
   SHA; `l` to (re)label in the TUI; labels render in the list and survive reorder.
5. **M5 — actions + safety.** `a`/`p`/`d` for apply/pop/drop with confirm prompts;
   `stash-stash push -m` wrapper that records labels. Update sidecar on every op.
6. **M6 — staleness nag + polish.** `--stale-days`, color buckets by age, header
   "gathering dust" banner, `--json` output for scripting, `go install` docs.

## 8. Backlog / future features (v0.2+)

1. **Auto-labels from branch + first changed file** ("payments: fix retry").
2. **`stash-stash search <term>`** — grep across all stash diffs at once.
3. **Stash → branch** in one key (`b`): `git stash branch` with the saved label.
4. **Expiry policy / "compost":** optionally auto-archive stashes older than N
   days into a `refs/stash-stash/archive` instead of dropping them.
5. **Conflict pre-flight:** before apply/pop, dry-run and warn if it'll conflict.
6. **Tags & filtering:** tag stashes (`wip`, `experiment`, `hotfix`) and filter.
7. **Cross-repo dashboard:** `stash-stash --all-repos ~/code` to find dusty
   stashes across every repo you own.
8. **Shell hook:** optional pre-checkout reminder if you're leaving dusty stashes.
9. **Notes per stash:** multi-line note attached to a stash (the "why").
10. **Export/import** the sidecar so teammates can share stash context (rare but
    handy for handoffs).
11. **`stash-stash doctor`** — detect orphaned metadata, dangling stash commits
    recoverable via reflog ("found work you thought you lost").
12. **Theming** via Lip Gloss profiles (light/dark/high-contrast).

## 9. Out of scope

- **Not a full git client.** No commit/branch/rebase/merge UI. Stash only.
- **No remote/sync service.** Metadata is local sidecar JSON; no accounts, no SaaS.
- **No libgit2/CGo.** We shell out to the user's `git`; we don't reimplement it.
- **No GUI / desktop app.** Terminal only.
- **No secret scanning.** That's a different tool; stashes are local by nature.
- **We never silently delete work.** Every destructive op requires explicit input.
