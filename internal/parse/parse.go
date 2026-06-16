// Package parse handles interpreting the first CLI argument (a commit hash or
// a GitHub Pull Request URL) and deriving branch names. It is pure logic with
// no side effects so it can be unit tested in isolation.
package parse

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Kind distinguishes the two supported source types.
type Kind int

const (
	// KindCommit is a single commit identified by a (possibly abbreviated) hash.
	KindCommit Kind = iota
	// KindPullRequest is a GitHub pull request identified by its URL.
	KindPullRequest
)

// PRRef identifies a GitHub pull request, including the host so the REST
// fallback can target github.com or an enterprise instance.
type PRRef struct {
	Host   string // e.g. "github.com" or "ghe.example.com"
	Owner  string
	Repo   string
	Number int
}

// Slug returns the "owner/repo" form used by the gh CLI.
func (p PRRef) Slug() string {
	return p.Owner + "/" + p.Repo
}

// Source is the parsed result of the first positional argument.
type Source struct {
	Kind   Kind
	Commit string // populated when Kind == KindCommit
	PR     PRRef  // populated when Kind == KindPullRequest
}

// commitHashRe matches a hex string between 4 (git's minimum abbreviation) and
// 40 (a full SHA-1) characters long.
var commitHashRe = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// Parse inspects arg and reports whether it is a commit hash or a PR URL.
func Parse(arg string) (Source, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Source{}, fmt.Errorf("empty source argument")
	}

	// Anything that looks like a URL is treated as a PR URL.
	if strings.Contains(arg, "://") || strings.HasPrefix(arg, "github.com/") {
		pr, err := parsePRURL(arg)
		if err != nil {
			return Source{}, err
		}
		return Source{Kind: KindPullRequest, PR: pr}, nil
	}

	if commitHashRe.MatchString(arg) {
		return Source{Kind: KindCommit, Commit: strings.ToLower(arg)}, nil
	}

	return Source{}, fmt.Errorf("argument %q is neither a valid commit hash nor a GitHub PR URL", arg)
}

// parsePRURL extracts owner/repo/number from a URL of the form
// https://<host>/<owner>/<repo>/pull/<n>.
func parsePRURL(raw string) (PRRef, error) {
	// Allow scheme-less "github.com/..." input for convenience.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return PRRef{}, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return PRRef{}, fmt.Errorf("invalid PR URL %q: missing host", raw)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return PRRef{}, fmt.Errorf("invalid PR URL %q: expected https://<host>/<owner>/<repo>/pull/<number>", raw)
	}

	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return PRRef{}, fmt.Errorf("invalid PR URL %q: %q is not a valid PR number", raw, parts[3])
	}

	return PRRef{
		Host:   u.Host,
		Owner:  parts[0],
		Repo:   parts[1],
		Number: number,
	}, nil
}

// BranchName derives the working branch name from a commit SHA. The SHA is
// expected to be a full 40-char hash but the function tolerates shorter input.
func BranchName(sha string) string {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) > 8 {
		sha = sha[:8]
	}
	return "cherry-pick/" + sha
}
