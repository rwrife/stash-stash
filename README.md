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

> **v0.1 status (M1):** the binary currently builds, prints `--version`, and
> reports a stub "no stashes found" message. The commands below are the target
> v0.1 surface and land across [milestones M2–M6](https://github.com/rwrife/stash-stash/issues?q=label%3Amilestone).

```bash
stash-stash --version       # print the version (works today)
stash-stash                 # open the TUI over the current repo's stashes (M3+)
stash-stash push -m "label" # stash with a label that actually sticks (M5)
stash-stash --stale-days 7  # flag anything older than a week (M6)
```

## How labels survive

Git can't name a stash well, so `stash-stash` keeps a tiny sidecar at
`.git/stash-stash.json` mapping each stash's content SHA → your label, tags, and
notes. Pop one, push another — the labels stay attached to the right work.

## License

MIT
