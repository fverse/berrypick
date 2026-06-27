package main

import (
	"reflect"
	"testing"

	"github.com/fverse/berrypick/internal/store"
)

func TestParseSerial(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{":2", 2, true},
		{":12", 12, true},
		{"  :3  ", 3, true}, // surrounding space tolerated
		{":0", 0, false},    // row numbers are 1-based
		{"2", 0, false},     // no colon → not a serial
		{"a1b2c3d4", 0, false},
		{"main.go:42", 0, false}, // file:line never starts with a colon
		{"::2", 0, false},
		{":2x", 0, false},
		{":", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSerial(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseSerial(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestQueuedTargets(t *testing.T) {
	st := stateFrom(t,
		store.Event{Event: store.Queued, ID: "a", To: "release/2.0"},
		store.Event{Event: store.Queued, ID: "a", To: "release/1.0"},
		store.Event{Event: store.Done, ID: "a", To: "release/3.0"},   // done → not queued
		store.Event{Event: store.Queued, ID: "b", To: "release/2.0"}, // other id → excluded
	)
	got := queuedTargets(st, "a")
	if want := []string{"release/2.0", "release/1.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("queuedTargets(a) = %v, want %v", got, want)
	}
	if got := queuedTargets(st, "missing"); got != nil {
		t.Errorf("queuedTargets(missing) = %v, want nil", got)
	}
}
