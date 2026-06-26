package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/github"
	"github.com/fverse/berrypick/internal/parse"
	"github.com/fverse/berrypick/internal/store"
)

// item is the resolved, human-readable identity of a thing to track.
type item struct {
	id      string
	typ     store.ItemType
	subject string
	author  string
}

// resolveItem resolves a todo source — a commit hash, a <file>:<line> blame
// reference, or a pull-request URL — into its tracking identity, fetching the
// subject and author so the log stays readable without re-fetching later. The
// resulting id matches what a later cherry-pick of the same source records, so a
// queued todo and its completion fold to one (id, target) key.
func resolveItem(src parse.Source) (item, error) {
	switch src.Kind {
	case parse.KindCommit:
		full, err := git.ResolveCommit(src.Commit)
		if err != nil {
			return item{}, err
		}
		author, subject, _ := git.CommitMeta(full)
		return item{id: short(full), typ: store.Commit, subject: subject, author: author}, nil
	case parse.KindBlame:
		full, err := git.BlameLine(src.Blame.File, src.Blame.Line, "")
		if err != nil {
			return item{}, err
		}
		author, subject, _ := git.CommitMeta(full)
		return item{id: short(full), typ: store.Commit, subject: subject, author: author}, nil
	case parse.KindPullRequest:
		res, err := github.NewResolver().PRCommits(src.PR)
		if err != nil {
			return item{}, fmt.Errorf("resolving PR commits: %w", err)
		}
		// The PR's tip commit labels the change; its author is filled in only when
		// the commit is already local (todos are often queued before fetching).
		subject := ""
		if n := len(res.Commits); n > 0 {
			subject = res.Commits[n-1].Subject
		}
		author := ""
		if a, _, err := git.CommitMeta(res.HeadSHA); err == nil {
			author = a
		}
		return item{id: strconv.Itoa(src.PR.Number), typ: store.Change, subject: subject, author: author}, nil
	}
	return item{}, fmt.Errorf("unsupported source kind")
}

// repoRoot verifies we are inside a git repository and returns its top-level
// path, which anchors the shared .berrypick/ directory regardless of the current
// working directory within the repo.
func repoRoot() (string, error) {
	if !git.IsRepo() {
		return "", fmt.Errorf("not inside a git repository; run this from within your repo")
	}
	root, err := git.RepoRoot()
	if err != nil {
		return "", fmt.Errorf("finding repository root: %w", err)
	}
	return root, nil
}

// trackingID derives the logical tracking identity for a source. Commits and
// blamed lines are tracked by their short SHA; a pull request is tracked as a
// single change by its number, so a queued PR todo and its later cherry-pick
// fold to the same (id, target) key.
func trackingID(src parse.Source, tipSHA string) (id string, typ store.ItemType) {
	if src.Kind == parse.KindPullRequest {
		return strconv.Itoa(src.PR.Number), store.Change
	}
	return short(tipSHA), store.Commit
}

// recordCherryPickDone appends a `done` event for a completed cherry-pick, but
// only when the repo has an initialized .berrypick/ — tracking is opt-in. It is
// best-effort: the cherry-pick has already succeeded, so a failure to update the
// log is a warning, never a hard error. The source branch (from) is filled in
// from the config topology when it can be determined.
func recordCherryPickDone(root string, ev store.Event) {
	if !config.Exists(root) {
		return
	}
	ev.Event = store.Done
	if ev.From == "" {
		if c, err := config.Load(root); err == nil {
			ev.From = c.SourceFor(ev.To)
		}
	}
	if err := store.AppendEvent(config.LogPath(root), ev); err != nil {
		fmt.Fprintln(os.Stderr, warn("warning: cherry-pick succeeded but updating the tracking log failed: "+err.Error()))
		return
	}
	fmt.Printf("  Tracked:       %s → %s recorded as done in %s\n", ev.ID, ev.To, filepath.Join(config.Dir, config.LogName))
}
