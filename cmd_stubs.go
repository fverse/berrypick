package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// TEMPORARY: these are placeholders so the command tree compiles while the
// tracking commands are built one at a time. Each is replaced by a real
// implementation in its own file.

func newTodoCmd() *cobra.Command {
	return &cobra.Command{Use: "todo", Short: "Manage cherry-pick todos", RunE: func(*cobra.Command, []string) error {
		return fmt.Errorf("not yet implemented")
	}}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show cherry-pick status", RunE: func(*cobra.Command, []string) error {
		return fmt.Errorf("not yet implemented")
	}}
}

func newCompactCmd() *cobra.Command {
	return &cobra.Command{Use: "compact", Short: "Compact the event log", RunE: func(*cobra.Command, []string) error {
		return fmt.Errorf("not yet implemented")
	}}
}
