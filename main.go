// Command berrypick cherry-picks commits from a commit hash or a GitHub pull
// request onto a fresh branch created off a target branch.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/github"
	"github.com/fverse/berrypick/internal/parse"
	"github.com/spf13/cobra"
)

const remote = "origin"

// version is the build version, stamped at release time via -ldflags.
var version = "dev"

// errReported signals that the error was already printed (e.g. an arg error
// shown above the help), so main should just exit non-zero without printing it.
var errReported = errors.New("error already reported")

func main() {
	if err := newRootCmd().Execute(); err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		push        bool
		force       bool
		mainline    int
		deleteLocal bool
		rev         string
	)

	cmd := &cobra.Command{
		Use:     "berrypick <commit-hash | PR-url | file:line> <target-branch> [branch-name]",
		Version: version,
		Short:   "Cherry-pick commits from a commit, GitHub PR, or blamed line onto a new branch",
		Long: `berrypick creates a branch off <target-branch> and cherry-picks the
requested work onto it.

The first argument is one of:
  - a git commit hash (full or abbreviated);
  - a GitHub pull request URL (https://github.com/<owner>/<repo>/pull/<n>) — every
    commit in the PR is cherry-picked individually, in chronological order;
  - a <file>:<line> reference (e.g. internal/git/git.go:42) — git blame finds the
    commit that last changed that line and cherry-picks it.

Note for <file>:<line>: this cherry-picks the ENTIRE commit that last touched the
line, not just the line. If that commit changed 10 files, all 10 come along. The
line is blamed in your working tree by default; use --rev to blame a specific
revision instead.

By default the new branch is named cherry-pick/<first 8 chars of the SHA>, where
the SHA is the commit hash for a commit, the PR's HEAD (tip) commit SHA for a PR,
or the blamed commit's SHA for a <file>:<line> reference. Pass an optional third
argument to name the branch yourself instead.`,
		Example: `  berrypick a1b2c3d4e5f6 release/1.2
  berrypick https://github.com/owner/repo/pull/123 main
  berrypick internal/git/git.go:42 main
  berrypick a1b2c3d4e5f6 release/1.2 my-custom-branch`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Wrong number of arguments: print the error first, then usage,
			// examples and flags (cmd.Usage omits the long description).
			if len(args) < 2 || len(args) > 3 {
				fmt.Fprintln(os.Stderr, red(fmt.Sprintf("error: requires 2 or 3 arguments: <commit-hash | PR-url | file:line> <target-branch> [branch-name] (got %d)", len(args))))
				fmt.Fprintln(os.Stderr)
				_ = cmd.Usage()
				return errReported
			}
			if deleteLocal && !push {
				return fmt.Errorf("--delete-local requires --push (the local branch is only deleted after a successful push)")
			}
			branchName := ""
			if len(args) == 3 {
				branchName = args[2]
			}
			return run(args[0], args[1], branchName, options{push: push, force: force, mainline: mainline, deleteLocal: deleteLocal, rev: rev})
		},
	}

	cmd.Flags().BoolVar(&push, "push", false, "push the new branch to origin after a successful cherry-pick")
	cmd.Flags().BoolVar(&force, "force", false, "recreate the branch if it already exists")
	cmd.Flags().IntVarP(&mainline, "mainline", "m", 0, "parent number (1-based) to follow when cherry-picking a merge commit")
	cmd.Flags().BoolVar(&deleteLocal, "delete-local", false, "delete the local cherry-pick branch after a successful push (requires --push)")
	cmd.Flags().StringVar(&rev, "rev", "", "for a <file>:<line> source, blame this revision instead of the working tree")
	return cmd
}

type options struct {
	push        bool
	force       bool
	mainline    int
	deleteLocal bool
	rev         string
}

func run(sourceArg, targetBranch, branchName string, opts options) error {
	if !git.IsRepo() {
		return fmt.Errorf("not inside a git repository; run this from within your repo")
	}

	src, err := parse.Parse(sourceArg)
	if err != nil {
		return err
	}

	if opts.rev != "" && src.Kind != parse.KindBlame {
		return fmt.Errorf("--rev only applies to a <file>:<line> source")
	}

	// Resolve the commits to apply and the SHA used to name the branch.
	var (
		commits []github.Commit
		nameSHA string
	)
	switch src.Kind {
	case parse.KindCommit:
		full, err := git.ResolveCommit(src.Commit)
		if err != nil {
			return err
		}
		nameSHA = full
		commits = []github.Commit{{SHA: full}}
	case parse.KindBlame:
		where := "your working tree"
		if opts.rev != "" {
			where = opts.rev
		}
		fmt.Printf("Blaming %s:%d in %s...\n", src.Blame.File, src.Blame.Line, where)
		full, err := git.BlameLine(src.Blame.File, src.Blame.Line, opts.rev)
		if err != nil {
			return err
		}
		// Label with the commit subject so the user sees what they're bringing.
		subject, _ := git.CommitSubject(full)
		nameSHA = full
		commits = []github.Commit{{SHA: full, Subject: subject}}
		fmt.Printf("Line %d was last changed by %s — cherry-picking that entire commit (all files it touched).\n", src.Blame.Line, label(full, subject))
	case parse.KindPullRequest:
		fmt.Printf("Fetching commits for PR #%d (%s)...\n", src.PR.Number, src.PR.Slug())
		res, err := github.NewResolver().PRCommits(src.PR)
		if err != nil {
			return fmt.Errorf("resolving PR commits: %w", err)
		}
		if len(res.Commits) == 0 {
			return fmt.Errorf("PR #%d has no commits to cherry-pick", src.PR.Number)
		}
		if res.HeadSHA == "" {
			return fmt.Errorf("could not determine HEAD commit SHA for PR #%d", src.PR.Number)
		}
		nameSHA = res.HeadSHA
		commits = res.Commits
	}

	// Use the caller-supplied branch name when given; otherwise derive the
	// default cherry-pick/<8-char-sha> name from the resolved SHA.
	branch := strings.TrimSpace(branchName)
	if branch == "" {
		branch = parse.BranchName(nameSHA)
	}

	if git.LocalBranchExists(branch) && !opts.force {
		return fmt.Errorf("branch %q already exists; pass --force to recreate it", branch)
	}

	// Decide where to branch from. Prefer the remote target, fetched fresh so we
	// build on the latest; otherwise fall back to a local-only branch of the same
	// name and warn later that it isn't on origin yet.
	targetOnOrigin, err := git.RemoteBranchExists(remote, targetBranch)
	if err != nil {
		return fmt.Errorf("checking %s/%s: %w", remote, targetBranch, err)
	}

	var startPoint string
	switch {
	case targetOnOrigin:
		fmt.Printf("Fetching %s/%s...\n", remote, targetBranch)
		if err := git.Fetch(remote, targetBranch); err != nil {
			return err
		}
		startPoint = remote + "/" + targetBranch
	case git.LocalBranchExists(targetBranch):
		startPoint = targetBranch
		fmt.Printf("%s/%s not found; branching from your local %q instead.\n", remote, targetBranch, targetBranch)
	default:
		return fmt.Errorf("branch %q does not exist on %s or locally", targetBranch, remote)
	}

	// Remember where we started so we can return here if asked to delete the
	// cherry-pick branch afterward (you can't delete the branch you're on).
	startRef, _ := git.CurrentRef()

	fmt.Printf("Creating branch %q from %s...\n", branch, startPoint)
	if err := git.CreateBranch(branch, startPoint, opts.force); err != nil {
		return err
	}

	// Apply each commit in order. On conflict, stop and explain how to recover.
	for i, c := range commits {
		// Merge commits can't be cherry-picked without telling git which parent
		// is the mainline. Detect this up front and give actionable guidance
		// rather than letting git fail with a cryptic error.
		parents, err := git.ParentCount(c.SHA)
		if err != nil {
			return err
		}
		if parents > 1 && opts.mainline == 0 {
			return fmt.Errorf("%s is a merge commit (%d parents); re-run with --mainline <n> to pick the diff relative to one parent (usually --mainline 1)", short(c.SHA), parents)
		}

		fmt.Printf("Cherry-picking [%d/%d] %s\n", i+1, len(commits), label(c.SHA, c.Subject))
		if err := git.CherryPick(c.SHA, opts.mainline); err != nil {
			// Only show conflict-resolution steps if git actually stopped on a
			// conflict; other failures already printed their own reason.
			if git.InProgress() {
				printConflictHelp()
			}
			return fmt.Errorf("cherry-pick failed on %s", short(c.SHA))
		}
	}

	deletedLocal := false
	if opts.push {
		fmt.Printf("Pushing %q to %s...\n", branch, remote)
		if err := git.Push(remote, branch); err != nil {
			return err
		}

		// Only after a successful push: optionally delete the local branch. Return
		// to where we started first, since git won't delete the current branch.
		if opts.deleteLocal {
			back := startRef
			if back == "" || back == branch {
				back = startPoint
			}
			fmt.Printf("Switching to %s and deleting local branch %q...\n", back, branch)
			if err := git.Checkout(back); err != nil {
				return err
			}
			if err := git.DeleteBranch(branch); err != nil {
				return err
			}
			deletedLocal = true
		}
	}

	// Best-effort: build the web PR-creation link from the origin remote. The
	// cherry-pick branch is the head (it holds the picked commits).
	prURL := ""
	if u, err := git.RemoteURL(remote); err == nil {
		if repo, err := parse.RemoteURL(u); err == nil {
			prURL = repo.NewPRURL(branch)
		}
	}

	printSummary(branch, targetBranch, commits, opts.push, targetOnOrigin, deletedLocal, prURL)
	return nil
}

func printConflictHelp() {
	fmt.Fprintln(os.Stderr, `
The cherry-pick stopped and the repository is paused mid-operation (see git's
output above for the reason). To proceed:

  - If there are conflicts, resolve them, then:   git add <files>
    and continue:                                 git cherry-pick --continue
  - To skip this commit:                          git cherry-pick --skip
  - To undo and return to the previous state:     git cherry-pick --abort`)
}

func printSummary(branch, target string, commits []github.Commit, pushed, targetOnOrigin, deletedLocal bool, prURL string) {
	fmt.Println()
	fmt.Println(green("✓ Done."))
	fmt.Printf("  Branch:        %s (off %s)\n", branch, baseLabel(target, targetOnOrigin))
	fmt.Printf("  Commits:       %d applied\n", len(commits))
	for _, c := range commits {
		if c.Subject != "" {
			fmt.Printf("    - %s %s\n", short(c.SHA), c.Subject)
		} else {
			fmt.Printf("    - %s\n", short(c.SHA))
		}
	}

	fmt.Println()
	fmt.Println("Next steps:")

	// The base branch must exist on origin for a PR to be openable.
	if !targetOnOrigin {
		fmt.Println(warn(fmt.Sprintf("  warning: base branch %q is not on %s yet — push it first so the PR has a base:", target, remote)))
		fmt.Printf("    git push --set-upstream %s %s\n", remote, target)
	}

	if pushed {
		if deletedLocal {
			fmt.Printf("  Branch pushed to %s and local copy deleted. Open a PR:\n", remote)
		} else {
			fmt.Printf("  Branch pushed to %s. Open a PR:\n", remote)
		}
	} else {
		fmt.Println("  Push the branch, then open a PR:")
		fmt.Printf("    git push --set-upstream %s %s\n", remote, branch)
	}
	if prURL != "" {
		fmt.Printf("    %s\n", link(prURL))
	} else {
		// Fall back to the gh command when the remote can't be parsed.
		fmt.Printf("    gh pr create --base %s --head %s\n", target, branch)
	}
}

func baseLabel(target string, onOrigin bool) string {
	if onOrigin {
		return remote + "/" + target
	}
	return "local " + target
}

// colorizeStream wraps s in the given ANSI SGR code, but only when the target
// stream is a terminal and NO_COLOR is unset, so piped or redirected output
// stays clean.
func colorizeStream(f *os.File, code, s string) string {
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// green marks success; link is bold cyan;
// warn is yellow. These print to stdout. red is for errors
// on stderr.
func green(s string) string { return colorizeStream(os.Stdout, "32", s) }
func link(s string) string  { return colorizeStream(os.Stdout, "1;36", s) }
func warn(s string) string  { return colorizeStream(os.Stdout, "33", s) }
func red(s string) string   { return colorizeStream(os.Stderr, "31", s) }

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// label renders a commit for display: "<short-sha> <subject>" when a subject is
// known, otherwise the full SHA.
func label(sha, subject string) string {
	if subject != "" {
		return fmt.Sprintf("%s %s", short(sha), subject)
	}
	return sha
}
