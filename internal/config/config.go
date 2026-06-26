// Package config loads, validates and scaffolds the shared, committed
// .berrypick/config.toml that describes a repository's back-port topology: which
// source branch changes flow from and which target branches they flow to. Two
// shapes are supported — a simple [branches] block for the common one-source,
// many-targets case, and an advanced [[flows]] list for chained back-ports. When
// [[flows]] is present it wins over [branches].
//
// Parsing is pure (it operates on a directory path, not git state) so it can be
// unit tested in isolation; branch-existence warnings are produced by Validate,
// which takes an injectable existence check rather than shelling out itself.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Layout constants for the shared, committed tracking directory.
const (
	// Dir is the per-repo directory holding the committed config and event log.
	Dir = ".berrypick"
	// ConfigName is the hand-edited, committed configuration file.
	ConfigName = "config.toml"
	// LogName is the append-only, committed event store.
	LogName = "log.jsonl"
)

// Branches is the simple one-source, many-targets form.
type Branches struct {
	Source  string   `toml:"source"`
	Targets []string `toml:"targets"`
}

// Flow is one edge of the advanced chained-back-port form: changes on From may
// be cherry-picked onto each branch in To.
type Flow struct {
	From string   `toml:"from"`
	To   []string `toml:"to"`
}

// Forge describes how to talk to the hosting provider. When absent the forge is
// autodetected from the git remote.
type Forge struct {
	Kind string `toml:"kind"` // github | gitlab | ... ; empty means autodetect
	Host string `toml:"host"`
}

// Config is the parsed .berrypick/config.toml.
type Config struct {
	Branches Branches `toml:"branches"`
	Flows    []Flow   `toml:"flows"`
	Forge    Forge    `toml:"forge"`
}

// DirPath returns the .berrypick directory inside root.
func DirPath(root string) string { return filepath.Join(root, Dir) }

// ConfigPath returns the config.toml path inside root's .berrypick directory.
func ConfigPath(root string) string { return filepath.Join(root, Dir, ConfigName) }

// LogPath returns the log.jsonl path inside root's .berrypick directory.
func LogPath(root string) string { return filepath.Join(root, Dir, LogName) }

// Exists reports whether root already has an initialized .berrypick directory.
func Exists(root string) bool {
	info, err := os.Stat(DirPath(root))
	return err == nil && info.IsDir()
}

// Load reads and parses root/.berrypick/config.toml. A missing directory yields
// a clear "run berrypick init" hint rather than an opaque file-not-found error.
func Load(root string) (*Config, error) {
	path := ConfigPath(root)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s found at %s; run `berrypick init` first", filepath.Join(Dir, ConfigName), DirPath(root))
		}
		return nil, fmt.Errorf("checking %s: %w", path, err)
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

// UsesFlows reports whether the advanced chained form is in effect. When true,
// [[flows]] wins and [branches] is ignored.
func (c *Config) UsesFlows() bool { return len(c.Flows) > 0 }

// Source returns the effective source branch for display. For the simple form it
// is [branches].source. For flows it is the graph root — a "from" that never
// appears as anyone's "to" — falling back to the first flow's "from".
func (c *Config) Source() string {
	if !c.UsesFlows() {
		return c.Branches.Source
	}
	isTarget := map[string]bool{}
	for _, f := range c.Flows {
		for _, t := range f.To {
			isTarget[t] = true
		}
	}
	for _, f := range c.Flows {
		if !isTarget[f.From] {
			return f.From
		}
	}
	return c.Flows[0].From
}

// Targets returns every configured target branch, in declaration order with
// duplicates removed. These are the columns of the status matrix.
func (c *Config) Targets() []string {
	if !c.UsesFlows() {
		return dedup(c.Branches.Targets)
	}
	var all []string
	for _, f := range c.Flows {
		all = append(all, f.To...)
	}
	return dedup(all)
}

// TargetsFrom returns the targets a change on source should be back-ported to.
// For the simple form that is every configured target. For flows it is every
// branch reachable from source by following from→to edges (transitively, so a
// chained main→2.0→1.0 topology back-ports to both 2.0 and 1.0 from main).
func (c *Config) TargetsFrom(source string) []string {
	if !c.UsesFlows() {
		return dedup(c.Branches.Targets)
	}
	edges := map[string][]string{}
	for _, f := range c.Flows {
		edges[f.From] = append(edges[f.From], f.To...)
	}
	var order []string
	seen := map[string]bool{source: true}
	stack := append([]string{}, edges[source]...)
	for len(stack) > 0 {
		b := stack[0]
		stack = stack[1:]
		if seen[b] {
			continue
		}
		seen[b] = true
		order = append(order, b)
		stack = append(stack, edges[b]...)
	}
	return order
}

// SourceFor returns the branch that target is back-ported from, per the
// configured topology: for [[flows]] it is the From of the flow whose To
// contains target; for [branches] it is the single source. It returns "" when
// the source can't be determined, so callers record from only when known.
func (c *Config) SourceFor(target string) string {
	if c.UsesFlows() {
		for _, f := range c.Flows {
			for _, t := range f.To {
				if t == target {
					return f.From
				}
			}
		}
		return ""
	}
	return c.Branches.Source
}

// BranchNames returns every distinct branch named anywhere in the config
// (sources and targets), for existence validation.
func (c *Config) BranchNames() []string {
	var names []string
	if c.UsesFlows() {
		for _, f := range c.Flows {
			names = append(names, f.From)
			names = append(names, f.To...)
		}
	} else {
		if c.Branches.Source != "" {
			names = append(names, c.Branches.Source)
		}
		names = append(names, c.Branches.Targets...)
	}
	return dedup(names)
}

// Validate returns human-readable warnings for configured branches that do not
// yet exist, using the injected exists check. Missing branches are intentionally
// not hard errors: config may be written ahead of branch creation. A nil/empty
// result means everything checks out.
func (c *Config) Validate(exists func(branch string) bool) []string {
	var warnings []string
	if !c.UsesFlows() && c.Branches.Source == "" {
		warnings = append(warnings, "no source branch configured under [branches]")
	}
	if len(c.Targets()) == 0 {
		warnings = append(warnings, "no target branches configured")
	}
	if exists != nil {
		for _, b := range c.BranchNames() {
			if b != "" && !exists(b) {
				warnings = append(warnings, fmt.Sprintf("branch %q does not exist yet", b))
			}
		}
	}
	return warnings
}

// dedup returns xs with empties dropped and later duplicates removed, preserving
// first-seen order.
func dedup(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
