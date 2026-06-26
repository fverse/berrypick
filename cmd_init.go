package main

import (
	"fmt"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/spf13/cobra"
)

// newInitCmd builds `berrypick init`, which scaffolds the shared, committed
// .berrypick/ directory (a prefilled config.toml plus an empty log.jsonl).
func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the shared .berrypick/ tracking directory",
		Long: `init creates .berrypick/ at the repository root with:

  - config.toml — a starter config (simple [branches] form) prefilled with the
    current branch as the source, ready for you to fill in the targets;
  - log.jsonl   — an empty append-only event log.

Both files are meant to be committed and shared by the team. init is idempotent:
if .berrypick/ already exists it is left untouched. Use --force to rewrite the
config scaffold (the event log is never discarded).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			if config.Exists(root) && !force {
				fmt.Printf("%s already exists at %s — nothing to do (use --force to rewrite the config scaffold).\n", config.Dir, config.DirPath(root))
				return nil
			}

			branch, _ := git.CurrentRef()
			if _, err := config.Scaffold(root, branch, force); err != nil {
				return err
			}

			fmt.Println(green("✓ Initialized " + config.Dir + "/"))
			fmt.Printf("  %s   — set your target branches under [branches]\n", config.ConfigName)
			fmt.Printf("  %s     — append-only event log\n", config.LogName)
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Printf("  1. Edit %s and list your target branches.\n", config.ConfigPath(root))
			fmt.Printf("  2. Commit %s/ so the whole team shares the same tracking.\n", config.Dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rewrite the config scaffold even if .berrypick/ already exists")
	return cmd
}
