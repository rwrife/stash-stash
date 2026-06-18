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

🚧 Early. v0.1 in progress — see [`PLAN.md`](./PLAN.md) and the
[milestones](https://github.com/rwrife/stash-stash/issues?q=label%3Amilestone).

## Install

```bash
go install github.com/rwrife/stash-stash/cmd/stash-stash@latest
```

(Requires Go 1.22+ and a working `git` on your PATH.)

## Usage

> **v0.1 status (M3):** the binary builds, prints `--version`, reads your real
> stashes, and — when stdout is a terminal — opens an **interactive TUI**: a
> scrollable list on the left and a live `git stash show -p` diff preview on the
> right. Piped or non-TTY output (and `--no-tui`) still prints the plain M2
> table, so scripts and CI keep working. Sidecar labels and mutating actions
> land across [milestones M4–M6](https://github.com/rwrife/stash-stash/issues?q=label%3Amilestone).

```bash
stash-stash --version       # print the version (works today)
stash-stash                 # interactive TUI: browse stashes + preview diffs (works today, M3)
stash-stash --no-tui        # force the plain table even on a TTY (works today)
stash-stash | cat           # piped/non-TTY → plain table automatically
stash-stash push -m "label" # stash with a label that actually sticks (M5)
stash-stash --stale-days 7  # flag anything older than a week (M6)
```

### Interactive TUI (M3)

Run `stash-stash` inside a repo with stashes and you get a two-pane browser:

- **Left:** every stash with its `stash@{N}` ref, age, subject, and origin branch.
- **Right:** the full unified diff (`git stash show -p`) for the selected stash,
  lightly colorized and scrollable.

Keys: `↑`/`↓` (or `j`/`k`) to select · `g`/`G` jump to top/bottom ·
`⏎`/`space`/`PgDn` and `PgUp` to scroll the diff · `q` / `Ctrl-C` / `Esc` to quit.
The layout is resize-aware. It's read-only — nothing is applied, popped, or
dropped in M3.

The plain (non-TTY / `--no-tui`) listing still looks like:

```
INDEX      SUBJECT                        AGE  BRANCH
stash@{0}  On main: new file work         2h   main
stash@{1}  WIP on main: c533301 init      5d   main
stash@{2}  On feature/x: half-done modal  23d  feature/x
```

## How labels survive

Git can't name a stash well, so `stash-stash` keeps a tiny sidecar at
`.git/stash-stash.json` mapping each stash's content SHA → your label, tags, and
notes. Pop one, push another — the labels stay attached to the right work.

## License

MIT
