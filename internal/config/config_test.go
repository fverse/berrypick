package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeConfig writes body to a fresh root/.berrypick/config.toml and returns root.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(DirPath(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(root), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadBranchesForm(t *testing.T) {
	root := writeConfig(t, `
[branches]
source  = "main"
targets = ["release/2.0", "release/1.0"]
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.UsesFlows() {
		t.Fatal("UsesFlows() = true, want false for [branches] form")
	}
	if c.Source() != "main" {
		t.Errorf("Source() = %q, want main", c.Source())
	}
	if want := []string{"release/2.0", "release/1.0"}; !reflect.DeepEqual(c.Targets(), want) {
		t.Errorf("Targets() = %v, want %v", c.Targets(), want)
	}
	if got := c.TargetsFrom("main"); !reflect.DeepEqual(got, []string{"release/2.0", "release/1.0"}) {
		t.Errorf("TargetsFrom(main) = %v, want all targets", got)
	}
}

func TestLoadFlowsForm(t *testing.T) {
	root := writeConfig(t, `
[branches]
source  = "ignored"
targets = ["ignored-target"]

[[flows]]
from = "main"
to   = ["release/2.0"]

[[flows]]
from = "release/2.0"
to   = ["release/1.0"]
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.UsesFlows() {
		t.Fatal("UsesFlows() = false, want true when [[flows]] present")
	}
	// Graph root is main (never appears as a target).
	if c.Source() != "main" {
		t.Errorf("Source() = %q, want main", c.Source())
	}
	if want := []string{"release/2.0", "release/1.0"}; !reflect.DeepEqual(c.Targets(), want) {
		t.Errorf("Targets() = %v, want %v", c.Targets(), want)
	}
	// Transitive reachability: from main you reach both 2.0 and 1.0.
	if got := c.TargetsFrom("main"); !reflect.DeepEqual(got, []string{"release/2.0", "release/1.0"}) {
		t.Errorf("TargetsFrom(main) = %v, want [release/2.0 release/1.0]", got)
	}
	// From an intermediate branch only its downstream targets are reachable.
	if got := c.TargetsFrom("release/2.0"); !reflect.DeepEqual(got, []string{"release/1.0"}) {
		t.Errorf("TargetsFrom(release/2.0) = %v, want [release/1.0]", got)
	}
}

func TestValidateMissingBranchWarns(t *testing.T) {
	root := writeConfig(t, `
[branches]
source  = "main"
targets = ["release/2.0", "release/1.0"]
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	exists := func(b string) bool { return b == "main" || b == "release/2.0" }
	warnings := c.Validate(exists)
	if len(warnings) != 1 {
		t.Fatalf("Validate warnings = %v, want exactly one (for release/1.0)", warnings)
	}
	// A missing branch must warn, never hard-fail (no error return at all).
}

func TestValidateNoTargets(t *testing.T) {
	root := writeConfig(t, `
[branches]
source  = "main"
targets = []
`)
	c, _ := Load(root)
	warnings := c.Validate(func(string) bool { return true })
	if len(warnings) == 0 {
		t.Fatal("Validate() with no targets returned no warnings, want one")
	}
}

func TestLoadMissingDir(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); err == nil {
		t.Fatal("Load on uninitialized repo: expected error, got nil")
	}
}

func TestScaffoldIdempotent(t *testing.T) {
	root := t.TempDir()

	created, err := Scaffold(root, "develop", false)
	if err != nil || !created {
		t.Fatalf("first Scaffold: created=%v err=%v, want created=true nil", created, err)
	}
	// Config should load and carry the current branch as source.
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load after scaffold: %v", err)
	}
	if c.Source() != "develop" {
		t.Errorf("scaffolded Source() = %q, want develop", c.Source())
	}
	if _, err := os.Stat(LogPath(root)); err != nil {
		t.Errorf("log.jsonl not created: %v", err)
	}

	// Second call without force is a no-op and must not clobber.
	created, err = Scaffold(root, "other", false)
	if err != nil || created {
		t.Fatalf("second Scaffold: created=%v err=%v, want created=false nil", created, err)
	}
}

func TestScaffoldForcePreservesLog(t *testing.T) {
	root := t.TempDir()
	if _, err := Scaffold(root, "main", false); err != nil {
		t.Fatal(err)
	}
	// Simulate recorded history.
	logLine := []byte(`{"event":"done","id":"abc"}` + "\n")
	if err := os.WriteFile(LogPath(root), logLine, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Scaffold(root, "main", true); err != nil {
		t.Fatalf("forced Scaffold: %v", err)
	}
	got, err := os.ReadFile(LogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(logLine) {
		t.Errorf("--force discarded log history: got %q, want %q", got, logLine)
	}
}

func TestPathHelpers(t *testing.T) {
	root := "/repo"
	if got := ConfigPath(root); got != filepath.Join("/repo", ".berrypick", "config.toml") {
		t.Errorf("ConfigPath = %q", got)
	}
	if got := LogPath(root); got != filepath.Join("/repo", ".berrypick", "log.jsonl") {
		t.Errorf("LogPath = %q", got)
	}
}
