package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/store"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var branch string
	var asJSON bool
	var reconcile bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the cherry-pick matrix across target branches",
		Long: `status folds the event log into a matrix: each tracked change is a row and each
configured target branch is a column. A cell is "✓ done" when the change has been
cherry-picked there, "⧗ todo" when it is queued, and "· -" when it is not tracked
for that branch.

--reconcile (kept behind the flag so plain status stays fast) additionally scans
each target branch's git history for cherry-pick -x annotations, surfacing picks
made outside berrypick and offering to backfill done events for them.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(branch, asJSON, reconcile)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "filter the matrix to a single target branch")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full matrix as JSON")
	cmd.Flags().BoolVar(&reconcile, "reconcile", false, "scan target branches for cherry-picks made outside berrypick")
	return cmd
}

func runStatus(branch string, asJSON, reconcile bool) error {
	root, c, err := loadTracking()
	if err != nil {
		return err
	}
	st, err := store.LoadState(config.LogPath(root))
	if err != nil {
		return err
	}

	// Columns are the configured targets, narrowed to one when --branch is given.
	targets := statusTargets(c, st)
	if branch != "" {
		targets = []string{branch}
	}

	if reconcile {
		return runReconcile(root, c, st, targets, asJSON)
	}

	m := st.Matrix(targets)
	if asJSON {
		return emitMatrixJSON(c.Source(), m)
	}
	renderMatrix(c.Source(), m)
	return nil
}

// statusTargets returns the matrix columns: every configured target in config
// order, followed by any other branch that has pending or done activity in the
// log. The trailing extras keep status consistent with `todo list` — a todo
// queued for an unconfigured branch (e.g. via `todo add --to`) still shows up
// instead of silently vanishing.
func statusTargets(c *config.Config, st *store.State) []string {
	order := c.Targets()
	seen := map[string]bool{}
	for _, t := range order {
		seen[t] = true
	}
	add := func(to string) {
		if to != "" && !seen[to] {
			seen[to] = true
			order = append(order, to)
		}
	}
	for _, ev := range st.PendingTodos() {
		add(ev.To)
	}
	for _, ev := range st.Done() {
		add(ev.To)
	}
	return order
}

// renderMatrix prints the status matrix as an aligned table.
func renderMatrix(source string, m store.Matrix) {
	fmt.Printf("Cherry-pick status (source: %s)\n\n", source)
	if len(m.Rows) == 0 {
		fmt.Println("No tracked changes yet. Queue some with `berrypick todo add`.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 3, ' ', 0)

	header := []string{"ID", "SUBJECT"}
	header = append(header, m.Targets...)
	fmt.Fprintln(tw, strings.Join(header, "\t"))

	for _, row := range m.Rows {
		cells := []string{row.ID, dash(row.Subject)}
		for _, t := range m.Targets {
			cells = append(cells, cellLabel(row.Cells[t]))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

// cellLabel renders a matrix cell for the terminal.
func cellLabel(s store.CellState) string {
	switch s {
	case store.CellDone:
		return "✓ done"
	case store.CellTodo:
		return "⧗ todo"
	default:
		return "· -"
	}
}

// cellName is the machine-readable cell value for --json.
func cellName(s store.CellState) string {
	switch s {
	case store.CellDone:
		return "done"
	case store.CellTodo:
		return "todo"
	default:
		return "none"
	}
}

func emitMatrixJSON(source string, m store.Matrix) error {
	type jsonRow struct {
		ID      string            `json:"id"`
		Type    string            `json:"type,omitempty"`
		Subject string            `json:"subject,omitempty"`
		Author  string            `json:"author,omitempty"`
		Cells   map[string]string `json:"cells"`
	}
	out := struct {
		Source  string    `json:"source"`
		Targets []string  `json:"targets"`
		Rows    []jsonRow `json:"rows"`
	}{Source: source, Targets: m.Targets, Rows: []jsonRow{}}

	for _, row := range m.Rows {
		cells := map[string]string{}
		for _, t := range m.Targets {
			cells[t] = cellName(row.Cells[t])
		}
		out.Rows = append(out.Rows, jsonRow{
			ID:      row.ID,
			Type:    string(row.Type),
			Subject: row.Subject,
			Author:  row.Author,
			Cells:   cells,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// runReconcile scans each target branch for cherry-pick -x annotations and
// surfaces any whose original commit berrypick has no done event for — i.e.
// picks made outside the tool. With --json it reports the findings; otherwise it
// offers to backfill done events so the matrix catches up.
func runReconcile(root string, c *config.Config, st *store.State, targets []string, asJSON bool) error {
	var external []store.Event
	for _, t := range targets {
		ref := scanRef(t)
		if ref == "" {
			if !asJSON {
				fmt.Println(warn(fmt.Sprintf("  %s: not found locally or on %s; skipping", t, remote)))
			}
			continue
		}
		recs, err := git.LogCherryPicks(ref)
		if err != nil {
			return fmt.Errorf("scanning %s: %w", ref, err)
		}
		for _, r := range recs {
			if coveredByDone(st, t, r) {
				continue
			}
			external = append(external, store.Event{
				Event:   store.Done,
				ID:      short(r.OrigSHA),
				Type:    store.Commit,
				To:      t,
				From:    c.SourceFor(t),
				Subject: r.Subject,
				NewSHA:  r.NewSHA,
			})
		}
	}

	if asJSON {
		if external == nil {
			external = []store.Event{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(external)
	}

	if len(external) == 0 {
		fmt.Println(green("✓ Reconcile: every -x cherry-pick on the target branches is already tracked."))
		return nil
	}

	fmt.Printf("Found %d cherry-pick(s) made outside berrypick:\n\n", len(external))
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, ev := range external {
		fmt.Fprintf(tw, "  %s\t→ %s\t%s\n", ev.ID, ev.To, dash(ev.Subject))
	}
	tw.Flush()

	if !confirm(fmt.Sprintf("\nBackfill %d done event(s) into the log?", len(external))) {
		fmt.Println("Left the log unchanged.")
		return nil
	}
	logPath := config.LogPath(root)
	for _, ev := range external {
		if err := store.AppendEvent(logPath, ev); err != nil {
			return err
		}
	}
	fmt.Println(green(fmt.Sprintf("✓ Backfilled %d done event(s).", len(external))))
	return nil
}

// coveredByDone reports whether berrypick already has a done event accounting for
// a -x annotated commit: either a done event keyed by the original short SHA, or
// any done event on the target whose resulting SHA matches (which also covers
// PR-tracked picks whose tip commit lands here).
func coveredByDone(st *store.State, target string, r git.CherryPickRecord) bool {
	if ev, ok := st.Latest(short(r.OrigSHA), target); ok && ev.Event == store.Done {
		return true
	}
	for _, ev := range st.Done() {
		if ev.To == target && ev.NewSHA != "" && ev.NewSHA == r.NewSHA {
			return true
		}
	}
	return false
}

// scanRef returns a local ref to scan for branch — the local branch if present,
// otherwise the origin remote-tracking ref — or "" when neither exists. It never
// touches the network.
func scanRef(branch string) string {
	if git.LocalBranchExists(branch) {
		return branch
	}
	if remoteRef := remote + "/" + branch; git.RefExists(remoteRef) {
		return remoteRef
	}
	return ""
}

// confirm asks a yes/no question on stdin, defaulting to no. A non-interactive
// stdin (EOF) is treated as no, so reconcile never blocks in scripts.
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		fmt.Println()
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "y" || answer == "yes"
}
