package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/parse"
	"github.com/fverse/berrypick/internal/store"
	"github.com/spf13/cobra"
)

// newTodoCmd builds `berrypick todo`, the manual queue of cherry-picks that still
// need to happen. Each (item, target) pair is its own logical todo so partial
// back-ports are trackable.
func newTodoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "Queue, list and remove pending cherry-picks",
		Long: `todo records which changes still need to be cherry-picked onto which target
branches. Todos are events in the shared log, so the whole team sees the same
backlog. Each (item, target) pair is tracked independently.`,
	}
	cmd.AddCommand(newTodoAddCmd(), newTodoListCmd(), newTodoRemoveCmd())
	return cmd
}

func newTodoAddCmd() *cobra.Command {
	var to []string
	cmd := &cobra.Command{
		Use:   "add <commit | PR-url | file:line>",
		Short: "Queue a change to be cherry-picked onto target branches",
		Long: `add appends a queued event for the given source onto each target branch. With
--to it queues only the named branches; without --to it defaults to every
configured target (for [[flows]], every target reachable from the source or your
current branch). The subject and author are resolved now so the list stays
readable without re-fetching. Already-queued or already-done pairs are skipped
rather than double-added.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTodoAdd(args[0], to)
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", nil, "target branch(es) to queue for (repeatable); defaults to all configured targets")
	return cmd
}

func runTodoAdd(sourceArg string, to []string) error {
	root, c, err := loadTracking()
	if err != nil {
		return err
	}

	src, err := parse.Parse(sourceArg)
	if err != nil {
		return err
	}
	it, err := resolveItem(src)
	if err != nil {
		return err
	}

	targets := to
	if len(targets) == 0 {
		targets = defaultTargets(c)
		if len(targets) == 0 {
			return fmt.Errorf("no target branches configured and none given; add targets to %s or pass --to", filepath.Join(config.Dir, config.ConfigName))
		}
	}

	logPath := config.LogPath(root)
	st, err := store.LoadState(logPath)
	if err != nil {
		return err
	}

	queue, skip := planQueue(st, it.id, targets)
	for _, t := range skip {
		ev, _ := st.Latest(it.id, t)
		fmt.Println(warn(fmt.Sprintf("  skip: %s → %s is already %s", it.id, t, ev.Event)))
	}
	for _, t := range queue {
		ev := store.Event{
			Event:   store.Queued,
			ID:      it.id,
			Type:    it.typ,
			To:      t,
			From:    c.SourceFor(t),
			Subject: it.subject,
			Author:  it.author,
		}
		if err := store.AppendEvent(logPath, ev); err != nil {
			return err
		}
		fmt.Printf("  queued: %s → %s\n", it.id, t)
	}

	fmt.Println()
	label := it.id
	if it.subject != "" {
		label = fmt.Sprintf("%s (%s)", it.id, it.subject)
	}
	fmt.Printf("%s — %d queued, %d skipped.\n", label, len(queue), len(skip))
	return nil
}

// planQueue decides, for each requested target, whether a new queued event
// should be appended or the pair skipped because its latest event is already
// queued or done. It is pure (state in, decision out) so the dedup rule is unit
// tested without touching git or the filesystem.
func planQueue(st *store.State, id string, targets []string) (queue, skip []string) {
	for _, t := range targets {
		if ev, ok := st.Latest(id, t); ok && (ev.Event == store.Queued || ev.Event == store.Done) {
			skip = append(skip, t)
			continue
		}
		queue = append(queue, t)
	}
	return queue, skip
}

// planRemove decides which targets a remove should record a removed event for.
// With an explicit target it is just that branch; otherwise it is every target
// where the change is currently a pending todo. An empty result with no explicit
// target is an error so the user gets a clear message.
func planRemove(st *store.State, id, to string) ([]string, error) {
	if to != "" {
		return []string{to}, nil
	}
	var targets []string
	for _, ev := range st.PendingTodos() {
		if ev.ID == id {
			targets = append(targets, ev.To)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no queued todo found for %q; pass --to <branch> to remove a specific (possibly done) entry", id)
	}
	return targets, nil
}

func newTodoListCmd() *cobra.Command {
	var branch string
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List pending cherry-pick todos, grouped by target branch",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTodoList(branch, asJSON)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "only show todos for this target branch")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON for scripting")
	return cmd
}

func runTodoList(branch string, asJSON bool) error {
	root, c, err := loadTracking()
	if err != nil {
		return err
	}
	st, err := store.LoadState(config.LogPath(root))
	if err != nil {
		return err
	}

	var todos []store.Event
	for _, ev := range st.PendingTodos() {
		if branch == "" || ev.To == branch {
			todos = append(todos, ev)
		}
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if todos == nil {
			todos = []store.Event{}
		}
		return enc.Encode(todos)
	}

	if len(todos) == 0 {
		fmt.Println("No pending todos.")
		return nil
	}

	fmt.Printf("Cherry-pick todos (source: %s)\n", c.Source())

	// Group by target, ordering branches as configured so output is stable; any
	// target present in the log but not in config is appended afterward.
	byTarget := map[string][]store.Event{}
	for _, ev := range todos {
		byTarget[ev.To] = append(byTarget[ev.To], ev)
	}
	for _, t := range orderedTargets(c, byTarget) {
		rows := byTarget[t]
		if len(rows) == 0 {
			continue
		}
		fmt.Printf("\n→ %s\n", t)
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		for _, ev := range rows {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", ev.ID, dash(ev.Subject), dash(ev.Author))
		}
		tw.Flush()
	}
	return nil
}

func newTodoRemoveCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Drop a todo by recording a removed event",
		Long: `remove appends a removed event for the change (never deleting log lines). With
--to it removes only that target; without --to it removes the change across every
target where it is currently queued.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTodoRemove(args[0], to)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "only remove for this target branch; default removes across all targets where queued")
	return cmd
}

func runTodoRemove(id, to string) error {
	root, _, err := loadTracking()
	if err != nil {
		return err
	}
	logPath := config.LogPath(root)
	st, err := store.LoadState(logPath)
	if err != nil {
		return err
	}

	targets, err := planRemove(st, id, to)
	if err != nil {
		return err
	}

	removed := 0
	for _, t := range targets {
		prev, ok := st.Latest(id, t)
		if !ok {
			fmt.Println(warn(fmt.Sprintf("  skip: nothing tracked for %s → %s", id, t)))
			continue
		}
		if prev.Event == store.Removed {
			fmt.Println(warn(fmt.Sprintf("  skip: %s → %s is already removed", id, t)))
			continue
		}
		// Carry identity forward so a later compaction keeps the entry readable.
		ev := store.Event{
			Event:   store.Removed,
			ID:      id,
			Type:    prev.Type,
			To:      t,
			From:    prev.From,
			Subject: prev.Subject,
			Author:  prev.Author,
		}
		if err := store.AppendEvent(logPath, ev); err != nil {
			return err
		}
		fmt.Printf("  removed: %s → %s\n", id, t)
		removed++
	}
	fmt.Println()
	fmt.Printf("%s — %d removed.\n", id, removed)
	return nil
}

// loadTracking resolves the repo root and loads the config, requiring that
// `berrypick init` has been run. Shared by all todo/status/compact commands.
func loadTracking() (string, *config.Config, error) {
	root, err := repoRoot()
	if err != nil {
		return "", nil, err
	}
	if !config.Exists(root) {
		return "", nil, fmt.Errorf("no %s found; run `berrypick init` first", config.Dir)
	}
	c, err := config.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, c, nil
}

// defaultTargets returns the targets a new todo should fan out to when --to is
// omitted: every branch reachable from the source, or from the current branch
// when it is itself a node in the configured topology.
func defaultTargets(c *config.Config) []string {
	from := c.Source()
	if cur, err := git.CurrentRef(); err == nil {
		for _, b := range c.BranchNames() {
			if b == cur {
				from = cur
				break
			}
		}
	}
	return c.TargetsFrom(from)
}

// orderedTargets lists target branches with configured targets first (in config
// order) followed by any extra targets present only in the log, so display order
// is stable and predictable.
func orderedTargets(c *config.Config, present map[string][]store.Event) []string {
	var order []string
	seen := map[string]bool{}
	for _, t := range c.Targets() {
		order = append(order, t)
		seen[t] = true
	}
	for t := range present {
		if !seen[t] {
			order = append(order, t)
			seen[t] = true
		}
	}
	return order
}

// dash renders an empty field as a centered dash for table readability.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
