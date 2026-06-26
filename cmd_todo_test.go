package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fverse/berrypick/internal/store"
)

// stateFrom builds a folded store.State from the given events via a temp log,
// so the planners are tested against real fold behavior.
func stateFrom(t *testing.T, events ...store.Event) *store.State {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	for i, ev := range events {
		if ev.At.IsZero() {
			ev.At = time.Unix(int64(i+1), 0).UTC()
		}
		if err := store.AppendEvent(path, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	st, err := store.LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return st
}

func TestPlanQueueDedup(t *testing.T) {
	st := stateFrom(t,
		store.Event{Event: store.Queued, ID: "a", To: "r1"},  // already queued → skip
		store.Event{Event: store.Done, ID: "a", To: "r2"},    // already done → skip
		store.Event{Event: store.Removed, ID: "a", To: "r3"}, // removed → re-queue allowed
	)
	// r4 has no prior event → queue.
	queue, skip := planQueue(st, "a", []string{"r1", "r2", "r3", "r4"})

	if want := []string{"r3", "r4"}; !reflect.DeepEqual(queue, want) {
		t.Errorf("queue = %v, want %v", queue, want)
	}
	if want := []string{"r1", "r2"}; !reflect.DeepEqual(skip, want) {
		t.Errorf("skip = %v, want %v", skip, want)
	}
}

func TestPlanRemoveAcrossTargets(t *testing.T) {
	st := stateFrom(t,
		store.Event{Event: store.Queued, ID: "a", To: "r1"},
		store.Event{Event: store.Queued, ID: "a", To: "r2"},
		store.Event{Event: store.Done, ID: "a", To: "r3"},   // done, not queued → not auto-removed
		store.Event{Event: store.Queued, ID: "b", To: "r1"}, // different id → untouched
	)

	// No --to: remove across all targets where `a` is currently queued.
	got, err := planRemove(st, "a", "")
	if err != nil {
		t.Fatalf("planRemove: %v", err)
	}
	if want := []string{"r1", "r2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("planRemove(a) = %v, want %v (done r3 excluded)", got, want)
	}

	// Explicit --to overrides and targets exactly that branch, even if done.
	got, err = planRemove(st, "a", "r3")
	if err != nil {
		t.Fatalf("planRemove --to: %v", err)
	}
	if want := []string{"r3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("planRemove(a, r3) = %v, want %v", got, want)
	}
}

func TestPlanRemoveNothingQueued(t *testing.T) {
	st := stateFrom(t, store.Event{Event: store.Done, ID: "a", To: "r1"})
	if _, err := planRemove(st, "a", ""); err == nil {
		t.Fatal("planRemove with no queued todos: expected error, got nil")
	}
}
