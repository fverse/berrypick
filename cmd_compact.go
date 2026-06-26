package main

import (
	"fmt"
	"path/filepath"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/store"
	"github.com/spf13/cobra"
)

func newCompactCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Collapse the event log to one line per tracked change",
		Long: `compact rewrites .berrypick/log.jsonl, keeping only the latest event for each
(id, target) pair. Derived state (todos, status) is unchanged — this only bounds
the log's growth over time.

Note: this rewrites the shared, committed log, so every line changes. Coordinate
it like any shared-file rewrite — compact, commit, and have teammates re-pull or
rebase — to avoid noisy merge conflicts.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompact(yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func runCompact(yes bool) error {
	root, _, err := loadTracking()
	if err != nil {
		return err
	}
	rel := filepath.Join(config.Dir, config.LogName)

	if !yes && !confirm(fmt.Sprintf("Rewrite the shared %s, collapsing it to the latest event per change?", rel)) {
		fmt.Println("Left the log unchanged.")
		return nil
	}

	stats, err := store.Compact(config.LogPath(root))
	if err != nil {
		return err
	}
	fmt.Println(green(fmt.Sprintf("✓ Compacted %s: %d → %d events.", rel, stats.Before, stats.After)))
	if stats.Before == stats.After {
		fmt.Println("  (already minimal — nothing to collapse)")
	} else {
		fmt.Println("  Commit the rewritten log and have teammates re-pull.")
	}
	return nil
}
