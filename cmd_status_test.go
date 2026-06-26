package main

import (
	"reflect"
	"testing"

	"github.com/fverse/berrypick/internal/config"
	"github.com/fverse/berrypick/internal/git"
	"github.com/fverse/berrypick/internal/store"
)

func TestStatusTargetsIncludesUnconfiguredTracked(t *testing.T) {
	// Config declares release/2.0; a todo was queued for the unconfigured
	// "develop" branch. status must still surface develop so it stays consistent
	// with `todo list`, with configured targets kept first.
	c := &config.Config{Branches: config.Branches{Source: "main", Targets: []string{"release/2.0"}}}
	st := stateFrom(t,
		store.Event{Event: store.Queued, ID: "a", To: "develop"},
		store.Event{Event: store.Done, ID: "b", To: "release/2.0"},
	)
	got := statusTargets(c, st)
	if want := []string{"release/2.0", "develop"}; !reflect.DeepEqual(got, want) {
		t.Errorf("statusTargets = %v, want %v (configured first, tracked extras appended)", got, want)
	}
}

func TestStatusTargetsSkipsRemovedOnlyBranch(t *testing.T) {
	// A branch whose only activity is a removed event has no pending/done state,
	// so it should not become a column.
	c := &config.Config{Branches: config.Branches{Source: "main", Targets: []string{"release/2.0"}}}
	st := stateFrom(t, store.Event{Event: store.Removed, ID: "a", To: "old-branch"})
	got := statusTargets(c, st)
	if want := []string{"release/2.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("statusTargets = %v, want %v (removed-only branch excluded)", got, want)
	}
}

func TestCellMarkersAndNames(t *testing.T) {
	cases := []struct {
		state  store.CellState
		marker string
		name   string
	}{
		{store.CellDone, "✓ done", "done"},
		{store.CellTodo, "⧗ todo", "todo"},
		{store.CellNone, "· -", "none"},
	}
	for _, c := range cases {
		if got := cellMarker(c.state); got != c.marker {
			t.Errorf("cellMarker(%v) = %q, want %q", c.state, got, c.marker)
		}
		if got := cellName(c.state); got != c.name {
			t.Errorf("cellName(%v) = %q, want %q", c.state, got, c.name)
		}
	}
}

func TestCoveredByDone(t *testing.T) {
	full := "9bfdb116aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	st := stateFrom(t,
		// Done keyed by the original short SHA (commit-mode pick).
		store.Event{Event: store.Done, ID: "9bfdb116", To: "r1", NewSHA: "newsha1"},
		// Done keyed by a PR number, but its resulting SHA is recorded.
		store.Event{Event: store.Done, ID: "123", Type: store.Change, To: "r2", NewSHA: "prtip"},
		// Only queued, not done.
		store.Event{Event: store.Queued, ID: "deadbeef", To: "r1"},
	)

	// Matched by original short SHA.
	if !coveredByDone(st, "r1", git.CherryPickRecord{OrigSHA: full, NewSHA: "newsha1"}) {
		t.Error("expected coverage by original short SHA")
	}
	// Matched by resulting SHA even when id is a PR number (no SHA match).
	if !coveredByDone(st, "r2", git.CherryPickRecord{OrigSHA: "ffffffffffffffff", NewSHA: "prtip"}) {
		t.Error("expected coverage by resulting new_sha")
	}
	// A queued-but-not-done pick is NOT covered (reconcile should surface it).
	if coveredByDone(st, "r1", git.CherryPickRecord{OrigSHA: "deadbeefcccccccc", NewSHA: "other"}) {
		t.Error("queued (not done) pick should not be considered covered")
	}
	// Unknown pick on the wrong branch is not covered.
	if coveredByDone(st, "r1", git.CherryPickRecord{OrigSHA: "0123456789abcdef", NewSHA: "prtip"}) {
		t.Error("new_sha match must be scoped to the same target branch")
	}
}
