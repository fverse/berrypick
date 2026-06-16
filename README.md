# berrypick

A small CLI that cherry-picks commits from a **commit hash** or a **GitHub Pull
Request** onto a fresh branch created off a target branch.

## Usage

```
berrypick <commit-hash | PR-url> <target-branch> [flags]
```

### Examples

```sh
# Cherry-pick a single commit onto a new branch off release/1.2
berrypick a1b2c3d4e5f6 release/1.2

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
| `-h`, `--help`       | Show help.                                                                              |

### Merge commits

A merge commit has more than one parent, so git needs to know which side to
treat as the mainline. `berrypick` detects this and stops with guidance; re-run
with `-m 1` (usually the target-branch parent) to cherry-pick it:

```sh
berrypick -m 1 9f1e5d1b2622d3538a0589ac2d76758b551a1340 dev
```

## GitHub access

The tool reads PR data in this order of preference:

1. **`gh` CLI** — used when `gh` is installed _and_ authenticated. It handles
   auth, pagination, and enterprise hosts automatically.
2. **GitHub REST API** — fallback when `gh` is unavailable. Reads a token from
   the `GITHUB_TOKEN` environment variable and talks to `api.github.com` (or
   `https://<host>/api/v3` for enterprise hosts) using only the standard library.

```sh
export GITHUB_TOKEN=ghp_xxx   # only needed for the REST fallback
```

## Error handling

- Fails if the current directory is not a git repository.
- Fails clearly if `<target-branch>` does not exist on `origin`.
- On a cherry-pick **conflict**, it stops and leaves the repo in the conflicted
  state, printing how to `git cherry-pick --continue` or `--abort`.
- Refuses to overwrite an existing branch unless `--force` is passed.
- Handles invalid PR URLs and unknown/ambiguous commit hashes gracefully.

All failures exit with a non-zero status code.

## Build

```sh
go build -o berrypick .
# or
make build
```

## Install

```sh
go install github.com/fverse/berrypick@latest
# or, from a clone:
make install
```

This places the `berrypick` binary in `$(go env GOPATH)/bin` — make sure that
directory is on your `PATH`.
