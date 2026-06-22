# stash-stash 🗄️

**A concierge for your git stash graveyard.**

`git stash` is a black hole. You stash "real quick," switch branches, and weeks
later you're staring at `stash@{6}` with no idea what's inside. `stash-stash`
turns that anonymous pile into a labeled, age-aware, browsable library — and
gently nags you to revive or bury the stashes rotting at the bottom.

```
┌ stash-stash ──────────────────────── 3 stashes gathering dust 🧹 ┐
│ > [payments] fix retry backoff      2h    main      +12 -3  2f   │
│   [wip] half-done modal             5d    feature/x +88 -1  4f   │
│   experiment: swap json lib         23d   main      +4  -4  1f   │
└──────────────────────────────────────────────────────────────────┘
  ↑/↓ move · enter preview · a apply · p pop · d drop · l label · q quit
```

## Why

`git stash list` gives you `stash@{2}: WIP on main: a1b2c3d`. Useless.
`stash-stash` gives you a real label, the age, the branch it was born on, and a
diffstat — skimmable in two seconds. It's not a full git client (use lazygit for
that); it does *one* thing: the stash lifecycle, done well.

## Status

🎉 **v0.1 — feature-complete.** All six milestones are in: read/list, an
interactive TUI with diff preview, sidecar labels, apply/pop/drop, a labeled
`push` wrapper, and the **staleness nag + `--json`** (M6). See
[`PLAN.md`](./PLAN.md) and the
[milestones](https://github.com/rwrife/stash-stash/issues?q=label%3Amilestone).

## Install

```bash
go install github.com/rwrife/stash-stash/cmd/stash-stash@latest
```

(Requires Go 1.22+ and a working `git` on your PATH.) This drops a single static
`stash-stash` binary in your `$(go env GOPATH)/bin` — make sure that's on your
`PATH`. Then run `stash-stash` inside any repo with stashes.

From source:

```bash
git clone https://github.com/rwrife/stash-stash
cd stash-stash
go build -o stash-stash ./cmd/stash-stash
```

## Usage

> **v0.1 (complete):** the binary reads your real stashes and — when stdout is
> a terminal — opens an **interactive TUI**: a scrollable list on the left and a
> live `git stash show -p` diff preview on the right. Each stash shows its
> **sidecar label** (or the raw git subject) plus a **diffstat**, and `l`
> (re)labels the selected stash; labels are keyed by content SHA so they survive
> pop/push reordering. **You can act on stashes**: `a` applies, `p` pops, and
> `d` drops the selected stash — pop and drop ask for a `y/N` confirm first, and
> the sidecar is kept in sync on every mutation. `stash-stash push -m "label"`
> stashes your working tree and records the label immediately. Stashes older
> than `--stale-days` (default 14) are **flagged as "gathering dust"** with a
> header banner and colored ages. Piped or non-TTY output (and `--no-tui`)
> prints the plain table, and `--json` emits a machine-readable list so scripts
> and CI keep working.

```bash
stash-stash                  # interactive TUI: browse, preview, (re)label, apply/pop/drop
stash-stash --version        # print the version
stash-stash --no-tui         # force the plain table even on a TTY
stash-stash | cat            # piped/non-TTY → plain table automatically
stash-stash push -m "label"  # stash with a label that actually sticks
stash-stash --stale-days 7   # flag anything older than a week as dusty
stash-stash --stale-days 0   # disable the staleness nag entirely
stash-stash --json | jq .    # machine-readable list for scripting
```

### Interactive TUI

Run `stash-stash` inside a repo with stashes and you get a two-pane browser:

- **Left:** every stash with its `stash@{N}` ref, age, **diffstat**
  (`+N -M · K files`), its **label** (highlighted) or raw subject, and origin
  branch.
- **Right:** the full unified diff (`git stash show -p`) for the selected stash,
  lightly colorized and scrollable.

Keys: `↑`/`↓` (or `j`/`k`) to select · `g`/`G` jump to top/bottom · **`l` to
(re)label** the selected stash (`⏎` saves, `esc` cancels) · **`a` apply ·
`p` pop · `d` drop** the selected stash · `⏎`/`space`/`PgDn` and `PgUp` to
scroll the diff · `q` / `Ctrl-C` / `Esc` to quit. The layout is resize-aware.

**Actions are safe by default.** `a` (apply) is non-destructive and runs
immediately. `p` (pop) and `d` (drop) move or delete work, so they pop a
`y/N` confirm first — nothing is removed without an explicit keypress. After a
pop or drop the list resyncs with git and the sidecar entry for the removed
stash is pruned, so labels never drift. Git's own errors (e.g. a conflicting
apply) are surfaced verbatim in a status toast, and the stash is left in place
so you can resolve it.

### Stashing with a label that sticks

```bash
stash-stash push -m "payments: retry backoff"
```

This is a thin wrapper around `git stash push -m`: it stashes your working tree
and, the instant the stash exists, records your label in the sidecar keyed by
the new stash's content SHA. No more `WIP on main:` mystery subjects — the
stash is named from birth. Run without `-m` and it behaves like a plain
`git stash push` (git picks the default subject). If the tree is clean it says
so and does nothing.

The plain (non-TTY / `--no-tui`) listing shows the same enrichment, with a
`*` marking dusty stashes and a banner when any are stale:

```
🧹 2 stashes are gathering dust (older than 14d) — triage them?
AGE   INDEX      LABEL                   BRANCH     CHANGES
2h    stash@{0}  On main: new file work  main       +8 -0 · 1 file
5d    stash@{1}  payments: retry fix     main       +12 -3 · 2 files
23d*  stash@{2}  On feature/x: modal     feature/x  +88 -1 · 4 files

(* = stale: older than 14d)
```

(`LABEL` is your sidecar label when set, otherwise the raw git subject.)

## Staleness nag

The whole point: stashes rot. Anything older than `--stale-days` (default **14**)
is flagged as *gathering dust*. In the TUI the age turns amber → orange → red as
it ages and the title bar nags ("🧹 N are gathering dust"); in the plain table
dusty rows get a `*` and a header banner. Tune it with `--stale-days N`, or pass
`--stale-days 0` to silence the nag entirely. Ages ramp through buckets:
*fresh* (< half the threshold) → *aging* → *stale* (≥ threshold) → *ancient*
(≥ 2× threshold).

## Scripting with `--json`

`stash-stash --json` prints a stable JSON document instead of the table — ideal
for `jq`, dashboards, or CI gates. It never opens the TUI and ignores TTY
detection:

```bash
# How many stashes are gathering dust? (non-zero exit you can gate on)
stash-stash --json | jq '.dusty_count'

# List the refs + labels of everything older than the threshold
stash-stash --json | jq -r '.stashes[] | select(.stale) | "\(.ref)\t\(.label // .subject)"'
```

Shape:

```json
{
  "stale_days": 14,
  "count": 3,
  "dusty_count": 1,
  "stashes": [
    {
      "index": 0,
      "ref": "stash@{0}",
      "sha": "a1b2c3d",
      "label": "payments: retry fix",
      "subject": "On main: retry fix",
      "branch": "main",
      "created": "2026-06-21T10:00:00Z",
      "age": "2h",
      "age_seconds": 7200,
      "staleness": "fresh",
      "stale": false,
      "diffstat": { "added": 12, "deleted": 3, "files": 2, "binary": false }
    }
  ]
}
```

## How labels survive

Git can't name a stash well, so `stash-stash` keeps a tiny sidecar at
`.git/stash-stash.json` mapping each stash's **content SHA** → your label (plus
tags and notes in later milestones). Because the key is the content SHA — not
the volatile `stash@{N}` index — popping one stash or pushing another keeps your
labels attached to the right work. Stale entries (for stashes dropped outside
`stash-stash`) are pruned automatically on the next run.

## License

MIT
