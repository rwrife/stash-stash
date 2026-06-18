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

> **v0.1 status (M2):** the binary builds, prints `--version`, and now reads
> your real stashes and prints them as a plain table (index · subject · age ·
> branch). The interactive TUI and the rest of the surface below land across
> [milestones M3–M6](https://github.com/rwrife/stash-stash/issues?q=label%3Amilestone).

```bash
stash-stash --version       # print the version (works today)
stash-stash                 # list the current repo's stashes as a table (works today, M2)
stash-stash                 # …becomes an interactive TUI in M3+
stash-stash push -m "label" # stash with a label that actually sticks (M5)
stash-stash --stale-days 7  # flag anything older than a week (M6)
```

Today the plain listing looks like:

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
