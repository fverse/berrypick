// Command cherry-pick cherry-picks commits from a commit hash or a GitHub pull
// request onto a fresh branch created off a target branch.
package main

import (
	"fmt"
	"os"

	"github.com/fverse/cherry-pick/internal/git"
	"github.com/fverse/cherry-pick/internal/github"
	"github.com/fverse/cherry-pick/internal/parse"
	"github.com/spf13/cobra"
)

const remote = "origin"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		push  bool
		force bool
	)

	cmd := &cobra.Command{
		Use:   "cherry-pick <commit-hash | PR-url> <target-branch>",
		Short: "Cherry-pick commits from a commit or GitHub PR onto a new branch",
		Long: `cherry-pick creates a branch off <target-branch> and cherry-picks the
requested work onto it.

The first argument is either a git commit hash (full or abbreviated) or a
GitHub pull request URL (https://github.com/<owner>/<repo>/pull/<n>). For a PR,
every commit in the PR is cherry-picked individually, in chronological order.

The new branch is named cherry-pick/<first 8 chars of the SHA>, where the SHA is
the commit hash for a commit, or the PR's HEAD (tip) commit SHA for a PR.`,
		Example: `  cherry-pick a1b2c3d4e5f6 release/1.2
  cherry-pick https://github.com/owner/repo/pull/123 main`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args[0], args[1], options{push: push, force: force})
		},
	}

	cmd.Flags().BoolVar(&push, "push", false, "push the new branch to origin after a successful cherry-pick")
	cmd.Flags().BoolVar(&force, "force", false, "recreate the branch if it already exists")
	return cmd
}

type options struct {
	push  bool
	force bool
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

	// Make sure the target branch exists on the remote before touching anything.
	exists, err := git.RemoteBranchExists(remote, targetBranch)
	if err != nil {
		return fmt.Errorf("checking %s/%s: %w", remote, targetBranch, err)
	}
	if !exists {
		return fmt.Errorf("branch %q does not exist on %s", targetBranch, remote)
	}

	if git.LocalBranchExists(branch) && !opts.force {
		return fmt.Errorf("branch %q already exists; pass --force to recreate it", branch)
	}

	fmt.Printf("Fetching %s/%s...\n", remote, targetBranch)
	if err := git.Fetch(remote, targetBranch); err != nil {
		return err
	}

	startPoint := remote + "/" + targetBranch
	fmt.Printf("Creating branch %q from %s...\n", branch, startPoint)
	if err := git.CreateBranch(branch, startPoint, opts.force); err != nil {
		return err
	}

	// Apply each commit in order. On conflict, stop and explain how to recover.
	for i, c := range commits {
		label := c.SHA
		if c.Subject != "" {
			label = fmt.Sprintf("%s %s", short(c.SHA), c.Subject)
		}
		fmt.Printf("Cherry-picking [%d/%d] %s\n", i+1, len(commits), label)
		if err := git.CherryPick(c.SHA); err != nil {
			printConflictHelp()
			return fmt.Errorf("cherry-pick failed on %s", short(c.SHA))
		}
	}

	if opts.push {
		fmt.Printf("Pushing %q to %s...\n", branch, remote)
		if err := git.Push(remote, branch); err != nil {
			return err
		}
	}

	printSummary(branch, targetBranch, commits, opts.push)
	return nil
}

func printConflictHelp() {
	fmt.Fprintln(os.Stderr, `
Cherry-pick stopped due to a conflict. The repository has been left in the
conflicted state. To proceed:

  1. Resolve the conflicts in the listed files.
  2. Stage them:           git add <files>
  3. Continue:             git cherry-pick --continue

Or, to undo and return to the previous state:

  git cherry-pick --abort`)
}

func printSummary(branch, target string, commits []github.Commit, pushed bool) {
	fmt.Println()
	fmt.Println("✓ Done.")
	fmt.Printf("  Branch:        %s (off %s/%s)\n", branch, remote, target)
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
	if pushed {
		fmt.Printf("  Branch pushed to %s. Open a PR with:\n", remote)
		fmt.Printf("    gh pr create --base %s --head %s\n", target, branch)
	} else {
		fmt.Println("  Push the branch and open a PR:")
		fmt.Printf("    git push --set-upstream %s %s\n", remote, branch)
		fmt.Printf("    gh pr create --base %s --head %s\n", target, branch)
	}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
