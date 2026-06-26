// Package git wraps the system git binary via os/exec. Commands that produce
// user-facing progress stream their stdout/stderr directly to the terminal;
// commands whose output we need to inspect capture it instead.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// run executes a git command, streaming stdout/stderr to the user so real git
// output (progress, errors, conflict notes) is visible.
func run(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// output executes a git command and returns its trimmed stdout. Stderr is
// folded into the error so failures stay legible.
func output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// IsRepo reports whether the current working directory is inside a git repo.
func IsRepo() bool {
	_, err := output("rev-parse", "--is-inside-work-tree")
	return err == nil
}

// RepoRoot returns the absolute path to the top level of the working tree, used
// to anchor the shared .berrypick/ directory at a stable location regardless of
// the current working directory within the repo.
func RepoRoot() (string, error) {
	return output("rev-parse", "--show-toplevel")
}

// Fetch updates the given branch from the remote.
func Fetch(remote, branch string) error {
	return run("fetch", remote, branch)
}

// RemoteBranchExists reports whether branch exists on the remote.
func RemoteBranchExists(remote, branch string) (bool, error) {
	out, err := output("ls-remote", "--heads", remote, branch)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// LocalBranchExists reports whether a local branch with the given name exists.
func LocalBranchExists(name string) bool {
	_, err := output("show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// ResolveCommit verifies that ref names exactly one commit and returns its full
// 40-char SHA. It fails for unknown or ambiguous references.
func ResolveCommit(ref string) (string, error) {
	sha, err := output("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || sha == "" {
		return "", fmt.Errorf("could not resolve commit %q (unknown or ambiguous); it may not exist locally — try fetching it first", ref)
	}
	return sha, nil
}

// BlameLine returns the full SHA of the commit that last modified the given line
// of file. When rev is non-empty the file is blamed at that revision instead of
// the working tree. It fails clearly when the line is uncommitted, out of range,
// or the file is missing/untracked (git's own stderr explains the latter cases).
func BlameLine(file string, line int, rev string) (string, error) {
	spec := strconv.Itoa(line) + "," + strconv.Itoa(line)
	args := []string{"blame", "-L", spec, "--porcelain"}
	if rev != "" {
		args = append(args, rev)
	}
	// The "--" guards against a path that looks like a flag or a revision.
	args = append(args, "--", file)

	out, err := output(args...)
	if err != nil {
		return "", err
	}

	// Porcelain output starts with "<40-hex-sha> <orig-line> <final-line> ...".
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("git blame produced no output for %s:%d", file, line)
	}
	sha := fields[0]
	if isAllZeroSHA(sha) {
		return "", fmt.Errorf("line %d of %s is not committed yet; commit it before cherry-picking", line, file)
	}
	return sha, nil
}

// CommitSubject returns the one-line subject of the given commit, used to label
// what is being brought along.
func CommitSubject(sha string) (string, error) {
	return output("show", "-s", "--format=%s", sha)
}

// isAllZeroSHA reports whether sha is git's all-zero "Not Committed Yet" marker.
func isAllZeroSHA(sha string) bool {
	if sha == "" {
		return false
	}
	return strings.Trim(sha, "0") == ""
}

// CreateBranch creates branch at startPoint and checks it out. When force is
// true an existing branch of the same name is reset to startPoint.
//
// --no-track is essential: startPoint is a remote-tracking ref (origin/<target>)
// and git would otherwise set the new branch's upstream to it. A later plain
// "git push" under push.default=upstream/tracking would then push onto the
// target branch (e.g. production) instead of the cherry-pick branch.
func CreateBranch(name, startPoint string, force bool) error {
	flag := "-b"
	if force {
		flag = "-B"
	}
	return run("checkout", "--no-track", flag, name, startPoint)
}

// CherryPick applies a single commit onto the current branch. When mainline is
// greater than zero it is passed as git's -m option, which is required (and only
// valid) when cherry-picking a merge commit.
//
// -x records "(cherry picked from commit <sha>)" in the new commit message,
// giving a git-native record of the original SHA that `status --reconcile` reads
// to detect picks made outside berrypick. Every current source mode has a real
// source SHA, so -x always applies.
func CherryPick(sha string, mainline int) error {
	args := []string{"cherry-pick", "-x"}
	if mainline > 0 {
		args = append(args, "-m", strconv.Itoa(mainline))
	}
	args = append(args, sha)
	return run(args...)
}

// HeadSHA returns the full SHA currently at HEAD, used to record the resulting
// commit on the target after a successful cherry-pick.
func HeadSHA() (string, error) {
	return output("rev-parse", "HEAD")
}

// CommitMeta returns the original author name and one-line subject of sha. Used
// to label a recorded cherry-pick with who wrote the change and what it does.
func CommitMeta(sha string) (author, subject string, err error) {
	// A NUL separator keeps a subject containing any character intact.
	out, err := output("show", "-s", "--format=%an%x00%s", sha)
	if err != nil {
		return "", "", err
	}
	if i := strings.IndexByte(out, 0); i >= 0 {
		return out[:i], out[i+1:], nil
	}
	return out, "", nil
}

// ParentCount returns the number of parent commits of sha. A result greater
// than one indicates a merge commit.
func ParentCount(sha string) (int, error) {
	// "<commit> <parent1> [<parent2> ...]"
	out, err := output("rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, fmt.Errorf("could not determine parents of %s", sha)
	}
	return len(fields) - 1, nil
}

// InProgress reports whether a cherry-pick is currently in progress (i.e. the
// last attempt stopped on a conflict, leaving CHERRY_PICK_HEAD behind).
func InProgress() bool {
	_, err := output("rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	return err == nil
}

// RemoteURL returns the configured URL of the given remote.
func RemoteURL(remote string) (string, error) {
	return output("remote", "get-url", remote)
}

// CurrentRef returns the name of the currently checked-out branch, or the commit
// SHA when HEAD is detached.
func CurrentRef() (string, error) {
	if name, err := output("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && name != "" {
		return name, nil
	}
	return output("rev-parse", "HEAD")
}

// Checkout switches the working tree to the given ref.
func Checkout(ref string) error {
	return run("checkout", ref)
}

// DeleteBranch force-deletes a local branch.
func DeleteBranch(name string) error {
	return run("branch", "-D", name)
}

// Push pushes branch to remote, setting upstream.
func Push(remote, branch string) error {
	return run("push", "--set-upstream", remote, branch)
}

// RefExists reports whether ref resolves to a commit in the local object store
// (no network), used to pick a scannable ref for reconciliation.
func RefExists(ref string) bool {
	_, err := output("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// CherryPickRecord is one commit on a branch that carries a git-native
// "(cherry picked from commit <sha>)" annotation left by cherry-pick -x.
type CherryPickRecord struct {
	NewSHA  string // the commit on the scanned branch
	Subject string // its subject line
	OrigSHA string // the source commit it was picked from (immediate source)
}

// cherryPickRe matches the -x annotation git appends to a cherry-picked commit.
var cherryPickRe = regexp.MustCompile(`cherry picked from commit ([0-9a-fA-F]{7,40})`)

// LogCherryPicks scans ref's history for commits annotated by cherry-pick -x and
// returns one record per commit. When a commit was picked through several
// branches it accumulates multiple annotations; the immediate source (the last
// annotation) is reported, matching what berrypick records for a chained pick.
func LogCherryPicks(ref string) ([]CherryPickRecord, error) {
	// Unit separator between fields, record separator between commits, so subjects
	// and multi-line bodies survive intact.
	out, err := output("log", ref, "--no-merges", "--grep", "cherry picked from commit", "--format=%H%x1f%s%x1f%b%x1e")
	if err != nil {
		return nil, err
	}
	var records []CherryPickRecord
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		matches := cherryPickRe.FindAllStringSubmatch(parts[2], -1)
		if len(matches) == 0 {
			continue
		}
		records = append(records, CherryPickRecord{
			NewSHA:  parts[0],
			Subject: parts[1],
			OrigSHA: matches[len(matches)-1][1],
		})
	}
	return records, nil
}
