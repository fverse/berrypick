# berrypick

A CLI that cherry-picks commits from a **commit hash** or the commit that last changed a **`<file>:<line>`** or a **GitHub Pull
Request** onto a fresh branch created off a target branch.

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
berrypick <commit-hash | file:line | PR-url> <target-branch> [flags]
```

The first argument can be:

- a **commit hash** (full or abbreviated) — cherry-picks that one commit;
  every commit in the PR, in chronological order;
- a **`<file>:<line>`** reference — uses `git blame` to find the commit that last
  changed that line and cherry-picks it (see [Blame mode](#blame-mode)).
- a **GitHub PR URL** (`https://github.com/<owner>/<repo>/pull/<n>`) — cherry-picks

The new branch is named `cherry-pick/<first 8 chars of the SHA>`.

### Examples

```sh
# Cherry-pick a single commit onto a new branch off release/1.2
berrypick a1b2c3d4e5f6 release/1.2

# Cherry-pick the commit that last changed line 42 of a file, onto main
berrypick src/utils/util.js:42 main

# Cherry-pick every commit in PR #123 onto a new branch off main, then push
berrypick https://github.com/owner/repo/pull/123 main --push

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
