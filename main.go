// Command berrypick cherry-picks commits from a commit hash or a GitHub pull
// request onto a fresh branch created off a target branch.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/github"
	"github.com/fverse/berrypick/internal/parse"
	"github.com/spf13/cobra"
)

const remote = "origin"

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
	)

	cmd := &cobra.Command{
		Use:   "berrypick <commit-hash | PR-url> <target-branch>",
		Short: "Cherry-pick commits from a commit or GitHub PR onto a new branch",
		Long: `berrypick creates a branch off <target-branch> and cherry-picks the
requested work onto it.

The first argument is either a git commit hash (full or abbreviated) or a
GitHub pull request URL (https://github.com/<owner>/<repo>/pull/<n>). For a PR,
every commit in the PR is cherry-picked individually, in chronological order.

The new branch is named cherry-pick/<first 8 chars of the SHA>, where the SHA is
the commit hash for a commit, or the PR's HEAD (tip) commit SHA for a PR.`,
		Example:       `  berrypick a1b2c3d4e5f6 release/1.2`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Wrong number of arguments: print the error first, then usage,
			// examples and flags (cmd.Usage omits the long description).
			if len(args) != 2 {
				fmt.Fprintln(os.Stderr, red(fmt.Sprintf("error: requires 2 arguments: <commit-hash | PR-url> <target-branch> (got %d)", len(args))))
				fmt.Fprintln(os.Stderr)
				_ = cmd.Usage()
				return errReported
			}
			if deleteLocal && !push {
				return fmt.Errorf("--delete-local requires --push (the local branch is only deleted after a successful push)")
			}
			return run(args[0], args[1], options{push: push, force: force, mainline: mainline, deleteLocal: deleteLocal})
		},
	}

	cmd.Flags().BoolVar(&push, "push", false, "push the new branch to origin after a successful cherry-pick")
	cmd.Flags().BoolVar(&force, "force", false, "recreate the branch if it already exists")
	cmd.Flags().IntVarP(&mainline, "mainline", "m", 0, "parent number (1-based) to follow when cherry-picking a merge commit")
	cmd.Flags().BoolVar(&deleteLocal, "delete-local", false, "delete the local cherry-pick branch after a successful push (requires --push)")
	return cmd
}

type options struct {
	push        bool
	force       bool
	mainline    int
	deleteLocal bool
}

func run(sourceArg, targetBranch string, opts options) error {
	if !git.IsRepo() {
		return fmt.Errorf("not inside a git repository; run this from within your repo")
	}

	src, err := parse.Parse(sourceArg)
	if err != nil {
		return err
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

	branch := parse.BranchName(nameSHA)

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

		label := c.SHA
		if c.Subject != "" {
			label = fmt.Sprintf("%s %s", short(c.SHA), c.Subject)
		}
		fmt.Printf("Cherry-picking [%d/%d] %s\n", i+1, len(commits), label)
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
