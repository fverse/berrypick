package config

import (
	"fmt"
	"os"
)

// scaffoldTemplate is the starter config.toml written by `berrypick init`. It
// uses the simple [branches] form, prefilled with the current branch as the
// source, and documents the advanced [[flows]] and [forge] blocks in comments so
// the file is self-explanatory without consulting the README.
const scaffoldTemplate = `# berrypick tracking config

# Simple form: changes flow from one source branch to many targets.
[branches]
source  = %q
targets = []  # e.g. ["release/2.0", "release/1.0"]

# Advanced form (optional): chained back-ports. If [[flows]] is present it wins
# over [branches]. Uncomment and edit to use it.
#
# [[flows]]
# from = "main"
# to   = ["release/2.0"]
#
# [[flows]]
# from = "release/2.0"
# to   = ["release/1.0"]

# Forge settings (optional): autodetected from the git remote when omitted.
#
# [forge]
# kind = "github"   # github | gitlab | ...
# host = "github.com"
`

// Scaffold creates root/.berrypick/ with a prefilled config.toml (source set to
// currentBranch) and an empty log.jsonl. When force is false and the directory
// already exists it makes no changes and returns created=false so the caller can
// print a notice; when force is true it overwrites the scaffold. The event log
// is only (re)created when missing, so --force never discards recorded history.
func Scaffold(root, currentBranch string, force bool) (created bool, err error) {
	dir := DirPath(root)
	if Exists(root) && !force {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}

	body := fmt.Sprintf(scaffoldTemplate, currentBranch)
	if err := os.WriteFile(ConfigPath(root), []byte(body), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", ConfigPath(root), err)
	}

	// Never truncate an existing log on --force: it is the shared history.
	if _, statErr := os.Stat(LogPath(root)); os.IsNotExist(statErr) {
		if err := os.WriteFile(LogPath(root), nil, 0o644); err != nil {
			return false, fmt.Errorf("writing %s: %w", LogPath(root), err)
		}
	}
	return true, nil
}
