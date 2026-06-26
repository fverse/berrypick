// Package store is the append-only event log behind berrypick's shared
// cherry-pick tracking. Every change is recorded as a new JSON line in
// .berrypick/log.jsonl — never an in-place edit — so two teammates recording
// picks the same day produce two lines, not a merge conflict. Current state is
// derived by folding the log: for each (id, target) key the latest event wins.
//
// All format and fold details live here so the rest of the program reasons about
// derived state (pending todos, completed picks, the status matrix) rather than
// JSON lines.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// EventKind is the verb of an event. The latest kind for a key decides its state.
type EventKind string

const (
	// Queued marks an (id, target) pair as a pending cherry-pick todo.
	Queued EventKind = "queued"
	// Done marks an (id, target) pair as successfully cherry-picked.
	Done EventKind = "done"
	// Removed drops/hides a previously queued or done pair.
	Removed EventKind = "removed"
)

// ItemType distinguishes a raw git commit from a forge change-request.
type ItemType string

const (
	// Commit is a change identified by a (short) commit SHA.
	Commit ItemType = "commit"
	// Change is a change identified by a forge change-request number/URL.
	Change ItemType = "change"
)

// Event is one immutable line in log.jsonl.
type Event struct {
	Event   EventKind `json:"event"`
	ID      string    `json:"id"`
	Type    ItemType  `json:"type,omitempty"`
	To      string    `json:"to"`
	From    string    `json:"from,omitempty"`
	Subject string    `json:"subject,omitempty"`
	Author  string    `json:"author,omitempty"`
	NewSHA  string    `json:"new_sha,omitempty"` // resulting SHA on target; only on Done
	At      time.Time `json:"at"`
}

// Key identifies the thing whose state the log tracks: a change on a target.
type Key struct {
	ID string
	To string
}

func (e Event) Key() Key { return Key{ID: e.ID, To: e.To} }

// AppendEvent appends ev to the log as a single JSON line, creating the file if
// needed. Appends are the only mutation; existing lines are never rewritten.
func AppendEvent(logPath string, ev Event) error {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("appending to %s: %w", logPath, err)
	}
	return nil
}

// State is the folded view of the log: the winning event per key, plus the order
// in which keys first appeared for stable output.
type State struct {
	latest map[Key]Event
	order  []Key
}

// LoadState reads logPath and folds it into current state. A missing log is
// treated as empty (the repo may be freshly initialized). Malformed lines are a
// hard error so corruption is surfaced rather than silently dropped.
func LoadState(logPath string) (*State, error) {
	st := &State{latest: map[Key]Event{}}

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", logPath, n, err)
		}
		st.apply(ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", logPath, err)
	}
	return st, nil
}

// apply folds one event into the state. The latest event wins per key, where
// "latest" is the greatest At timestamp; ties (and equal timestamps) resolve to
// the later line, since the log is read in append order. This keeps state
// correct even when a merge interleaves two contributors' lines out of order.
func (s *State) apply(ev Event) {
	k := ev.Key()
	prev, ok := s.latest[k]
	if !ok {
		s.order = append(s.order, k)
		s.latest[k] = ev
		return
	}
	if !ev.At.Before(prev.At) {
		s.latest[k] = ev
	}
}

// Latest returns the winning event for (id, to) and whether one exists.
func (s *State) Latest(id, to string) (Event, bool) {
	ev, ok := s.latest[Key{ID: id, To: to}]
	return ev, ok
}

// PendingTodos returns the events whose latest kind is Queued, in first-seen
// order.
func (s *State) PendingTodos() []Event { return s.filter(Queued) }

// Done returns the events whose latest kind is Done, in first-seen order.
func (s *State) Done() []Event { return s.filter(Done) }

func (s *State) filter(kind EventKind) []Event {
	var out []Event
	for _, k := range s.order {
		if ev := s.latest[k]; ev.Event == kind {
			out = append(out, ev)
		}
	}
	return out
}

// CellState is a cell in the status matrix.
type CellState int

const (
	// CellNone means the change is not tracked for that branch (latest event is
	// Removed, or there is none).
	CellNone CellState = iota
	// CellTodo means the latest event for the cell is Queued.
	CellTodo
	// CellDone means the latest event for the cell is Done.
	CellDone
)

// Row is one tracked change across the configured target branches.
type Row struct {
	ID      string
	Type    ItemType
	Subject string
	Author  string
	Cells   map[string]CellState
}

// Matrix is the folded status view: tracked changes as rows, target branches as
// columns.
type Matrix struct {
	Targets []string
	Rows    []Row
}

// Matrix builds the status matrix over the given target branches. Rows are the
// union of changes that are done or pending on at least one of those targets;
// each row carries that change's subject/author (resolved at queue/pick time)
// and a cell state per target. Rows are ordered by their first appearance in the
// log so output is stable.
func (s *State) Matrix(targets []string) Matrix {
	// Group winning events by ID, remembering first-seen order of IDs.
	byID := map[string][]Event{}
	var idOrder []string
	for _, k := range s.order {
		ev := s.latest[k]
		if _, seen := byID[ev.ID]; !seen {
			idOrder = append(idOrder, ev.ID)
		}
		byID[ev.ID] = append(byID[ev.ID], ev)
	}

	m := Matrix{Targets: targets}
	for _, id := range idOrder {
		events := byID[id]
		row := Row{ID: id, Cells: map[string]CellState{}}
		// Carry the change's metadata from any event that has it.
		for _, ev := range events {
			if row.Type == "" {
				row.Type = ev.Type
			}
			if row.Subject == "" {
				row.Subject = ev.Subject
			}
			if row.Author == "" {
				row.Author = ev.Author
			}
		}
		tracked := false
		for _, t := range targets {
			cell := CellNone
			if ev, ok := s.latest[Key{ID: id, To: t}]; ok {
				switch ev.Event {
				case Done:
					cell = CellDone
				case Queued:
					cell = CellTodo
				}
			}
			row.Cells[t] = cell
			if cell != CellNone {
				tracked = true
			}
		}
		// Skip changes that are only tracked on branches outside this view (e.g.
		// when filtering to a single --branch column).
		if tracked {
			m.Rows = append(m.Rows, row)
		}
	}
	return m
}

// Stats reports the outcome of a Compact run.
type Stats struct {
	Before int // event lines read
	After  int // event lines kept (one per surviving key)
}

// Compact rewrites logPath keeping only the winning event per (id, to) key,
// bounding file growth. Kept events are written in chronological order. The
// rewrite is atomic (temp file + rename) so an interrupted compaction never
// leaves a truncated log.
func Compact(logPath string) (Stats, error) {
	before, err := countLines(logPath)
	if err != nil {
		return Stats{}, err
	}
	st, err := LoadState(logPath)
	if err != nil {
		return Stats{}, err
	}

	kept := make([]Event, 0, len(st.order))
	for _, k := range st.order {
		kept = append(kept, st.latest[k])
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if !kept[i].At.Equal(kept[j].At) {
			return kept[i].At.Before(kept[j].At)
		}
		if kept[i].ID != kept[j].ID {
			return kept[i].ID < kept[j].ID
		}
		return kept[i].To < kept[j].To
	})

	tmp, err := os.CreateTemp(filepath.Dir(logPath), ".log-*.tmp")
	if err != nil {
		return Stats{}, fmt.Errorf("creating temp log: %w", err)
	}
	tmpName := tmp.Name()
	w := bufio.NewWriter(tmp)
	for _, ev := range kept {
		line, err := json.Marshal(ev)
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return Stats{}, fmt.Errorf("encoding event: %w", err)
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return Stats{}, fmt.Errorf("writing temp log: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return Stats{}, fmt.Errorf("flushing temp log: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return Stats{}, fmt.Errorf("closing temp log: %w", err)
	}
	if err := os.Rename(tmpName, logPath); err != nil {
		os.Remove(tmpName)
		return Stats{}, fmt.Errorf("replacing %s: %w", logPath, err)
	}
	return Stats{Before: before, After: len(kept)}, nil
}

// countLines returns the number of non-empty lines in path, treating a missing
// file as zero.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		if len(trimSpace(sc.Bytes())) > 0 {
			n++
		}
	}
	return n, sc.Err()
}

// trimSpace trims ASCII whitespace from a byte slice without allocating, used to
// detect blank JSONL lines.
func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
