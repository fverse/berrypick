// Package git wraps the system git binary via os/exec. Commands that produce
// user-facing progress stream their stdout/stderr directly to the terminal;
// commands whose output we need to inspect capture it instead.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// CreateBranch creates branch at startPoint and checks it out. When force is
// true an existing branch of the same name is reset to startPoint.
func CreateBranch(name, startPoint string, force bool) error {
	flag := "-b"
	if force {
		flag = "-B"
	}
	return run("checkout", flag, name, startPoint)
}

// CherryPick applies a single commit onto the current branch. When mainline is
// greater than zero it is passed as git's -m option, which is required (and only
// valid) when cherry-picking a merge commit.
func CherryPick(sha string, mainline int) error {
	args := []string{"cherry-pick"}
	if mainline > 0 {
		args = append(args, "-m", strconv.Itoa(mainline))
	}
	args = append(args, sha)
	return run(args...)
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

// Push pushes branch to remote, setting upstream.
func Push(remote, branch string) error {
	return run("push", "--set-upstream", remote, branch)
}
