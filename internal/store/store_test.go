package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// at returns a fixed-base timestamp offset by n seconds, so fixtures have a
// well-defined chronological order independent of wall-clock time.
func at(n int) time.Time {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(n) * time.Second)
}

// newLog writes the given events to a fresh log file and returns its path.
func newLog(t *testing.T, events ...Event) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	for _, ev := range events {
		if err := AppendEvent(path, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	return path
}

func TestFoldQueuedDoneRemoved(t *testing.T) {
	path := newLog(t,
		Event{Event: Queued, ID: "a1b2c3d4", To: "release/2.0", At: at(1)},
		Event{Event: Done, ID: "a1b2c3d4", To: "release/2.0", NewSHA: "ffff", At: at(2)},
		Event{Event: Queued, ID: "e5f6a7b8", To: "release/1.0", At: at(3)},
		Event{Event: Removed, ID: "e5f6a7b8", To: "release/1.0", At: at(4)},
	)
	st, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// queued -> done resolves to done.
	if ev, ok := st.Latest("a1b2c3d4", "release/2.0"); !ok || ev.Event != Done {
		t.Errorf("a1b2c3d4 latest = %+v ok=%v, want Done", ev, ok)
	}
	// queued -> removed resolves to removed (hidden).
	if ev, ok := st.Latest("e5f6a7b8", "release/1.0"); !ok || ev.Event != Removed {
		t.Errorf("e5f6a7b8 latest = %+v ok=%v, want Removed", ev, ok)
	}

	if got := st.Done(); len(got) != 1 || got[0].ID != "a1b2c3d4" {
		t.Errorf("Done() = %+v, want one done a1b2c3d4", got)
	}
	if got := st.PendingTodos(); len(got) != 0 {
		t.Errorf("PendingTodos() = %+v, want none (the only queued item was removed)", got)
	}
}

func TestOutOfOrderTimestampsLatestWins(t *testing.T) {
	// A later line carrying an EARLIER timestamp (as a bad merge might produce)
	// must not override the newer state.
	path := newLog(t,
		Event{Event: Done, ID: "x", To: "r1", At: at(10)},
		Event{Event: Queued, ID: "x", To: "r1", At: at(5)}, // stale, appended later
	)
	st, _ := LoadState(path)
	if ev, _ := st.Latest("x", "r1"); ev.Event != Done {
		t.Errorf("latest = %v, want Done (newer timestamp wins over later-but-older line)", ev.Event)
	}
}

func TestMatrixConstruction(t *testing.T) {
	path := newLog(t,
		Event{Event: Done, ID: "a1b2c3d4", Type: Commit, Subject: "Fix null deref", To: "release/2.0", At: at(1)},
		Event{Event: Queued, ID: "a1b2c3d4", Type: Commit, Subject: "Fix null deref", To: "release/1.0", At: at(2)},
		Event{Event: Done, ID: "e5f6a7b8", Type: Commit, Subject: "Patch CVE", To: "release/2.0", At: at(3)},
		Event{Event: Done, ID: "e5f6a7b8", Type: Commit, Subject: "Patch CVE", To: "release/1.0", At: at(4)},
		// Tracked only on a branch outside the requested columns: excluded.
		Event{Event: Done, ID: "deadbeef", Subject: "Other", To: "release/9.9", At: at(5)},
	)
	st, _ := LoadState(path)
	m := st.Matrix([]string{"release/2.0", "release/1.0"})

	if len(m.Rows) != 2 {
		t.Fatalf("Matrix rows = %d, want 2 (deadbeef is off-column)", len(m.Rows))
	}
	r0 := m.Rows[0]
	if r0.ID != "a1b2c3d4" || r0.Subject != "Fix null deref" {
		t.Errorf("row0 = %+v, want a1b2c3d4 Fix null deref", r0)
	}
	if r0.Cells["release/2.0"] != CellDone || r0.Cells["release/1.0"] != CellTodo {
		t.Errorf("row0 cells = %v, want [done todo]", r0.Cells)
	}
	r1 := m.Rows[1]
	if r1.Cells["release/2.0"] != CellDone || r1.Cells["release/1.0"] != CellDone {
		t.Errorf("row1 cells = %v, want [done done]", r1.Cells)
	}
}

func TestMatrixRemovedCellIsNone(t *testing.T) {
	path := newLog(t,
		Event{Event: Queued, ID: "a", To: "r1", At: at(1)},
		Event{Event: Done, ID: "a", To: "r2", At: at(2)},
		Event{Event: Removed, ID: "a", To: "r1", At: at(3)},
	)
	st, _ := LoadState(path)
	m := st.Matrix([]string{"r1", "r2"})
	if len(m.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(m.Rows))
	}
	if m.Rows[0].Cells["r1"] != CellNone {
		t.Errorf("removed cell r1 = %v, want CellNone", m.Rows[0].Cells["r1"])
	}
	if m.Rows[0].Cells["r2"] != CellDone {
		t.Errorf("cell r2 = %v, want CellDone", m.Rows[0].Cells["r2"])
	}
}

func TestCompactKeepsLatestPerKey(t *testing.T) {
	path := newLog(t,
		Event{Event: Queued, ID: "a", To: "r1", At: at(1)},
		Event{Event: Done, ID: "a", To: "r1", At: at(2)},
		Event{Event: Queued, ID: "a", To: "r2", At: at(3)},
		Event{Event: Queued, ID: "b", To: "r1", At: at(4)},
		Event{Event: Removed, ID: "b", To: "r1", At: at(5)},
	)
	stats, err := Compact(path)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if stats.Before != 5 {
		t.Errorf("Before = %d, want 5", stats.Before)
	}
	// Three distinct keys: (a,r1), (a,r2), (b,r1).
	if stats.After != 3 {
		t.Errorf("After = %d, want 3", stats.After)
	}

	// State must be unchanged by compaction.
	st, _ := LoadState(path)
	if ev, _ := st.Latest("a", "r1"); ev.Event != Done {
		t.Errorf("(a,r1) = %v, want Done", ev.Event)
	}
	if ev, _ := st.Latest("b", "r1"); ev.Event != Removed {
		t.Errorf("(b,r1) = %v, want Removed", ev.Event)
	}
	if n, _ := countLines(path); n != 3 {
		t.Errorf("file now has %d lines, want 3", n)
	}
}

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if len(st.PendingTodos()) != 0 || len(st.Done()) != 0 {
		t.Error("missing log should fold to empty state")
	}
}

func TestLoadStateMalformedLineErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Fatal("LoadState on malformed line: expected error, got nil")
	}
}

func TestAppendSetsTimestamp(t *testing.T) {
	path := newLog(t, Event{Event: Queued, ID: "a", To: "r1"}) // zero At
	st, _ := LoadState(path)
	ev, _ := st.Latest("a", "r1")
	if ev.At.IsZero() {
		t.Error("AppendEvent did not stamp a default timestamp")
	}
}
