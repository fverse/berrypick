package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/parse"
	"github.com/fverse/berrypick/internal/store"
	"github.com/mattn/go-isatty"
)

// serialRe matches a ":N" status-row reference (e.g. ":2"). The leading colon
// keeps it unambiguous against commit hashes (hex, >= 4 chars) and against
// <file>:<line> refs (which require a non-empty file before the colon).
var serialRe = regexp.MustCompile(`^:([0-9]+)$`)

// parseSerial reports whether arg is a ":N" status-row reference and returns N.
func parseSerial(arg string) (int, bool) {
	m := serialRe.FindStringSubmatch(strings.TrimSpace(arg))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// pickRef is a reference resolved for cherry-picking: the argument to feed the
// normal flow plus the tracking id used to look up queued targets.
type pickRef struct {
	sourceArg string // commit hash, PR URL, or file:line to pass to run()
	id        string // tracking id (short SHA or PR number)
}

// runPick is the entry point for the root command. It extends the classic
// `berrypick <source> <target> [branch]` flow with two tracking conveniences:
// the source may be a ":N" status-row reference, and the target may be omitted
// when the source's todos make it unambiguous.
func runPick(args []string, opts options) error {
	if !git.IsRepo() {
		return fmt.Errorf("not inside a git repository; run this from within your repo")
	}

	sourceArg := args[0]
	target := ""
	if len(args) >= 2 {
		target = args[1]
	}
	branchName := ""
	if len(args) == 3 {
		branchName = args[2]
	}

	serial, isSerial := parseSerial(sourceArg)

	// Classic path — explicit source and target, no row reference — needs no
	// tracking and behaves exactly as before.
	if !isSerial && target != "" {
		return run(sourceArg, target, branchName, opts)
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	if !config.Exists(root) {
		if isSerial {
			return fmt.Errorf("%q is a status-row reference, which needs tracking; run `berrypick init` first (or pass a commit/PR and a target branch)", sourceArg)
		}
		return fmt.Errorf("a target branch is required: berrypick %s <target-branch>", sourceArg)
	}

	ref, err := resolvePickRef(root, sourceArg, serial, isSerial)
	if err != nil {
		return err
	}

	if target == "" {
		target, err = resolveTargetFromTodos(root, ref.id)
		if err != nil {
			return err
		}
	}
	return run(ref.sourceArg, target, branchName, opts)
}

// resolvePickRef turns the source argument into a pickRef. A ":N" reference is
// looked up against the same matrix `berrypick status` prints; anything else is
// a normal commit/PR/file:line whose tracking id is resolved directly.
func resolvePickRef(root, sourceArg string, serial int, isSerial bool) (pickRef, error) {
	if isSerial {
		c, err := config.Load(root)
		if err != nil {
			return pickRef{}, err
		}
		st, err := store.LoadState(config.LogPath(root))
		if err != nil {
			return pickRef{}, err
		}
		m := st.Matrix(statusTargets(c, st))
		if serial > len(m.Rows) {
			return pickRef{}, fmt.Errorf("no row %d in `berrypick status` (it has %d row(s))", serial, len(m.Rows))
		}
		row := m.Rows[serial-1]
		ref := pickRef{id: row.ID}
		if row.Type == store.Change {
			url, err := prURLFromRemote(row.ID)
			if err != nil {
				return pickRef{}, fmt.Errorf("rebuilding PR URL for #%s: %w", row.ID, err)
			}
			ref.sourceArg = url
		} else {
			ref.sourceArg = row.ID // a short SHA is a valid commit reference
		}
		return ref, nil
	}

	src, err := parse.Parse(sourceArg)
	if err != nil {
		return pickRef{}, err
	}
	id, _, err := resolveID(src)
	if err != nil {
		return pickRef{}, err
	}
	return pickRef{sourceArg: sourceArg, id: id}, nil
}

// resolveID resolves a parsed source to its tracking id without any network
// fetch, so target lookup stays cheap.
func resolveID(src parse.Source) (string, store.ItemType, error) {
	switch src.Kind {
	case parse.KindCommit:
		full, err := git.ResolveCommit(src.Commit)
		if err != nil {
			return "", "", err
		}
		return short(full), store.Commit, nil
	case parse.KindBlame:
		full, err := git.BlameLine(src.Blame.File, src.Blame.Line, "")
		if err != nil {
			return "", "", err
		}
		return short(full), store.Commit, nil
	case parse.KindPullRequest:
		return strconv.Itoa(src.PR.Number), store.Change, nil
	}
	return "", "", fmt.Errorf("unsupported source")
}

// queuedTargets returns every target branch where id is currently a pending todo.
func queuedTargets(st *store.State, id string) []string {
	var targets []string
	for _, ev := range st.PendingTodos() {
		if ev.ID == id {
			targets = append(targets, ev.To)
		}
	}
	return targets
}

// resolveTargetFromTodos picks the target branch for id from its pending todos:
// the sole queued target when there is one, an interactive choice when there are
// several, or an error when none are queued.
func resolveTargetFromTodos(root, id string) (string, error) {
	st, err := store.LoadState(config.LogPath(root))
	if err != nil {
		return "", err
	}
	targets := queuedTargets(st, id)
	switch len(targets) {
	case 0:
		return "", fmt.Errorf("no pending todo for %q; pass a target branch explicitly: berrypick %s <target-branch>", id, id)
	case 1:
		return targets[0], nil
	default:
		return chooseTarget(id, targets)
	}
}

// chooseTarget prompts the user to pick one of several queued targets. When
// stdin is not interactive it returns an error listing the options instead of
// blocking, so scripts fail fast.
func chooseTarget(id string, targets []string) (string, error) {
	if !stdinIsInteractive() {
		return "", fmt.Errorf("%q is queued for multiple branches (%s); specify one: berrypick %s <target-branch>", id, strings.Join(targets, ", "), id)
	}
	fmt.Printf("%q is queued for multiple branches:\n", id)
	for i, t := range targets {
		fmt.Printf("  %d) %s\n", i+1, t)
	}
	fmt.Print("Pick a number: ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("no target selected")
	}
	choice := strings.TrimSpace(sc.Text())
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(targets) {
		return "", fmt.Errorf("invalid selection %q", choice)
	}
	return targets[n-1], nil
}

// prURLFromRemote rebuilds a pull-request URL from the origin remote and a PR
// number, so a PR todo (which stores only the number) can be re-picked by row.
func prURLFromRemote(number string) (string, error) {
	u, err := git.RemoteURL(remote)
	if err != nil {
		return "", err
	}
	repo, err := parse.RemoteURL(u)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s/%s/%s/pull/%s", repo.Host, repo.Owner, repo.Name, number), nil
}

// stdinIsInteractive reports whether stdin is a real terminal, so prompts are
// only shown when a human can answer them. A genuine isatty check is needed
// here: character-device detection alone would treat /dev/null (common in
// scripts and CI) as interactive and hang on a prompt.
func stdinIsInteractive() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
