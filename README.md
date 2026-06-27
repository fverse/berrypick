# berrypick

**When the same change has to land on several long-lived branches, `berrypick`
moves the work over and tracks where it still needs to go.** Teams that maintain
more than one branch in parallel constantly re-apply the same commits by hand and
lose track of which branches got which change. `berrypick` does the move for you
and keeps a **shared, committed record** of what's been ported and what's still
pending: queue the picks a branch needs, see an at-a-glance matrix of done versus
todo across every branch, and have each pick record itself automatically — so
nothing slips through and the whole team works from the same picture.
[Jump to tracking →](#tracking-cherry-picks-across-branches)

It does the cherry-picking. Point it at a **commit hash**, a
**`<file>:<line>`** (the commit that last changed that line), or a **GitHub Pull
Request**, and it cherry-picks the work onto a fresh branch off your target —
recording the result as it goes.

## Install

### macOS / Linux

```sh
curl -fsSL https://raw.githubusercontent.com/fverse/berrypick/main/install.sh | sh
```

This downloads the **latest** release for your OS and architecture and installs
it to `/usr/local/bin`.

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/fverse/berrypick/main/install.ps1 | iex
```

### With Go

```sh
go install github.com/fverse/berrypick@latest
```

### Manual download

Grab a prebuilt archive for your platform from the
[latest release](https://github.com/fverse/berrypick/releases/latest) — builds
are provided for **macOS**, **Linux**, and **Windows** (`amd64` and `arm64`).

## Usage

```
berrypick <commit-hash | file:line | PR-url> <target-branch> [branch-name] [flags]
```

The first argument can be:

- a **commit hash** (full or abbreviated) — cherry-picks that one commit;
  every commit in the PR, in chronological order;
- a **`<file>:<line>`** reference — uses `git blame` to find the commit that last
  changed that line and cherry-picks it (see [Blame mode](#blame-mode)).
- a **GitHub PR URL** (`https://github.com/<owner>/<repo>/pull/<n>`) — cherry-picks

By default the new branch is named `cherry-pick/<first 8 chars of the SHA>`. Pass
an optional third argument to name the branch yourself instead.

### Examples

```sh
# Cherry-pick a single commit onto a new branch off release/1.2
berrypick a1b2c3d4e5f6 release/1.2

# Cherry-pick the commit that last changed line 42 of a file, onto main
berrypick src/utils/util.js:42 main

# Cherry-pick every commit in PR #123 onto a new branch off main, then push
berrypick https://github.com/owner/repo/pull/123 main --push

# Cherry-pick a commit onto a new branch off release/1.2 with a custom branch name
berrypick a1b2c3d4e5f6 release/1.2 hotfix/login-bug

```

### Flags

| Flag                 | Description                                                                             |
| -------------------- | --------------------------------------------------------------------------------------- |
| `--push`             | Push the new branch to `origin` after a successful cherry-pick.                         |
| `--delete-local`     | Delete the local cherry-pick branch after a successful push. Requires `--push`.         |
| `--force`            | Recreate the working branch if it already exists.                                       |
| `-m`, `--mainline N` | Parent number (1-based) to follow when cherry-picking a **merge commit** (e.g. `-m 1`). |
| `--rev <ref>`        | For a `<file>:<line>` source, blame this revision instead of the working tree.          |
| `-h`, `--help`       | Show help.                                                                              |

### Merge commits

A merge commit has more than one parent, so git needs to know which side to
treat as the mainline. `berrypick` detects this and stops with guidance; re-run
with `-m 1` (usually the target-branch parent) to cherry-pick it:

```sh
berrypick -m 1 9f1e5d1b2622d3538a0589ac2d76758b551a1340 dev
```

### Blame mode

Pass a `<file>:<line>` reference to cherry-pick the commit that last modified
that line, as reported by `git blame`:

```sh
berrypick internal/git/git.go:42 main

# Blame a specific revision instead of the working tree
berrypick internal/git/git.go:42 main --rev origin/main
```

> **This cherry-picks the _entire_ commit that last touched the line, not just
> the line.** If that commit changed 10 files, all 10 come along.

The line is blamed in your working tree by default; use `--rev <ref>` to blame a
specific commit or branch. It fails clearly when the line is not committed yet,
the line number is out of range, or the file is missing or untracked.

## Tracking cherry-picks across branches

For teams that back-port the same fixes onto several branches, `berrypick`
can track what still needs picking and what's already done — in a **shared,
committed** log so everyone sees the same picture.

```sh
berrypick init          # scaffold .berrypick/ (config.toml + log.jsonl)
```

This creates a `.berrypick/` directory at the repo root:

```
.berrypick/
  config.toml     # hand-edited, committed: your branch topology
  log.jsonl       # append-only event log, committed
```

**Commit both files.** The log is append-only (one line per event) precisely so
two teammates recording picks the same day produce two lines, not a merge
conflict.

### `config.toml`

Simple form — one source, many targets:

```toml
[branches]
source  = "main"
targets = ["release/2.0", "release/1.0"]
```

Advanced form — chained back-ports. If `[[flows]]` is present it wins over
`[branches]`:

```toml
[[flows]]
from = "main"
to   = ["release/2.0"]

[[flows]]
from = "release/2.0"
to   = ["release/1.0"]
```

Optional forge block (autodetected from the remote when omitted):

```toml
[forge]
kind = "github"      # github | gitlab | ...
host = "github.com"
```

A configured branch that doesn't exist yet only warns — you can write the config
ahead of creating the branches.

### `berrypick todo`

Track which picks still need to happen:

```sh
# Queue a change onto every configured target (or pass --to to pick branches)
berrypick todo add a1b2c3d4
berrypick todo add https://github.com/owner/repo/pull/123 --to release/2.0
berrypick todo add internal/git/git.go:42

berrypick todo list                     # grouped by target branch
berrypick todo list --branch release/2.0
berrypick todo list --json              # for scripting

berrypick todo remove a1b2c3d4          # across all targets where it's queued
berrypick todo remove a1b2c3d4 --to release/1.0
```

Each `(item, target)` pair is its own todo, so partial back-ports are trackable.
Subject and author are resolved when you add, so the list reads well without
re-fetching. Adding an already-queued or already-done pair is skipped, not
double-added.

### `berrypick status`

Fold the log into a matrix — rows are tracked changes, columns are target
branches:

```sh
berrypick status
```

```
Cherry-pick status (source: main)

#   ID         SUBJECT                release/2.0   release/1.0
─────────────────────────────────────────────────────────────────
1   a1b2c3d4   Fix null deref         ✓ done        ⧗ todo
2   e5f6a7b8   Patch CVE-2026-1234    ✓ done        ✓ done
```

| Cell     | Meaning                                     |
| -------- | ------------------------------------------- |
| `✓ done` | latest event for `(id, branch)` is **done** |
| `⧗ todo` | latest event is **queued**                  |
| `· -`    | not tracked for that branch                 |

The `#` column gives each row a number you can pick by (see below).

```sh
berrypick status --branch release/2.0   # one column
berrypick status --json                 # full matrix as JSON
berrypick status --reconcile            # see below
```

`--reconcile` scans each target branch's history for `cherry picked from commit`
(`-x`) annotations and surfaces picks made **outside** berrypick, offering to
backfill `done` events for them. It's behind the flag so plain `status` stays
fast.

### Cherry-picking straight from a todo

Once a change is queued, you can pick it without retyping the SHA and target. The
ordinary `berrypick` command gains two tracking shortcuts:

```sh
berrypick :2            # pick row #2 from `berrypick status`
berrypick a1b2c3d4      # pick a queued change onto the branch it's queued for
```

- A **`:N`** argument refers to row `N` in `berrypick status` (the leading colon
  keeps it distinct from a commit hash). Row numbers are positional — they shift
  as todos are added or completed, so they mean "the list I'm looking at now,"
  not a stable id.
- The **target branch is optional**: when the change is queued for exactly one
  branch, that branch is used; when it's queued for several, you're asked which
  (or pass the branch explicitly, e.g. `berrypick :2 release/1.0`).

The pick runs the normal cherry-pick flow, so it records the `done` event
automatically — the todo flips from `⧗ todo` to `✓ done`.

### `berrypick compact`

```sh
berrypick compact
```

Rewrites `log.jsonl` keeping only the latest event per `(id, target)` key, to
bound growth. Derived state is unchanged. Because this rewrites the **shared**
committed log, coordinate it like any shared-file rewrite (compact, commit, have
teammates re-pull).

## GitHub access

For PR sources, the tool talks to the GitHub REST API and discovers credentials
automatically, with **no `gh` binary required at runtime**:

- A token from the `GH_TOKEN` / `GITHUB_TOKEN` environment variable, **or** the
  token stored by a previous `gh auth login` — whichever is found first.
- Enterprise hosts (`https://<host>/api/v3`) are handled automatically.
- **Public repositories work with no credentials at all** (subject to GitHub's
  low anonymous rate limit). A token is only required for **private** repos.

```sh
export GITHUB_TOKEN=ghp_xxx   # for private repos, if you haven't run `gh auth login`
```

## Build

From a clone:

```sh
go build -o berrypick .   # or: make build
make install              # builds and installs into $(go env GOPATH)/bin
```
