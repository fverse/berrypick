// Package github resolves the commits belonging to a pull request. It prefers
// the gh CLI (which handles auth, pagination and enterprise hosts) and falls
// back to the GitHub REST API using only the standard library.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/fverse/berrypick/internal/parse"
)

// Commit is a single commit in a pull request.
type Commit struct {
	SHA     string
	Subject string
}

// PRResult holds everything needed to act on a pull request: the tip (HEAD)
// commit SHA and the full list of commits in chronological order (oldest first).
type PRResult struct {
	HeadSHA string
	Commits []Commit
}

// Resolver fetches pull request data. It is an interface so callers can be
// tested with a fake implementation.
type Resolver interface {
	PRCommits(pr parse.PRRef) (PRResult, error)
}

// NewResolver picks the best available backend: the gh CLI when it is installed
// and authenticated, otherwise the REST API with a GITHUB_TOKEN.
func NewResolver() Resolver {
	if path, err := exec.LookPath("gh"); err == nil && ghAuthenticated() {
		return &ghResolver{bin: path}
	}
	return &restResolver{client: &http.Client{Timeout: 30 * time.Second}}
}

func ghAuthenticated() bool {
	cmd := exec.Command("gh", "auth", "status")
	return cmd.Run() == nil
}

// --- gh CLI backend ---

type ghResolver struct {
	bin string
}

func (g *ghResolver) PRCommits(pr parse.PRRef) (PRResult, error) {
	cmd := exec.Command(g.bin, "pr", "view", strconv.Itoa(pr.Number),
		"--repo", pr.Slug(), "--json", "commits,headRefOid")
	// gh resolves enterprise hosts via GH_HOST.
	if pr.Host != "" && pr.Host != "github.com" {
		cmd.Env = append(os.Environ(), "GH_HOST="+pr.Host)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return PRResult{}, fmt.Errorf("gh pr view #%d: %s", pr.Number, trimErr(stderr.String(), err))
	}

	var payload struct {
		HeadRefOid string `json:"headRefOid"`
		Commits    []struct {
			OID             string `json:"oid"`
			MessageHeadline string `json:"messageHeadline"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return PRResult{}, fmt.Errorf("parsing gh output for PR #%d: %w", pr.Number, err)
	}

	res := PRResult{HeadSHA: payload.HeadRefOid}
	for _, c := range payload.Commits {
		res.Commits = append(res.Commits, Commit{SHA: c.OID, Subject: c.MessageHeadline})
	}
	if res.HeadSHA == "" && len(res.Commits) > 0 {
		res.HeadSHA = res.Commits[len(res.Commits)-1].SHA
	}
	return res, nil
}

// --- REST API backend ---

type restResolver struct {
	client *http.Client
}

func (r *restResolver) PRCommits(pr parse.PRRef) (PRResult, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return PRResult{}, fmt.Errorf("gh CLI is unavailable and GITHUB_TOKEN is not set; install/authenticate gh or export a token")
	}
	base := apiBase(pr.Host)

	// HEAD SHA from the pull request object.
	var pull struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	pullURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", base, pr.Owner, pr.Repo, pr.Number)
	if err := r.getJSON(pullURL, token, &pull); err != nil {
		return PRResult{}, err
	}

	// Commits, paginated, oldest first (GitHub's documented order).
	var commits []Commit
	page := 1
	for {
		var batch []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/commits?per_page=100&page=%d",
			base, pr.Owner, pr.Repo, pr.Number, page)
		if err := r.getJSON(url, token, &batch); err != nil {
			return PRResult{}, err
		}
		for _, c := range batch {
			commits = append(commits, Commit{SHA: c.SHA, Subject: firstLine(c.Commit.Message)})
		}
		if len(batch) < 100 {
			break
		}
		page++
	}

	return PRResult{HeadSHA: pull.Head.SHA, Commits: commits}, nil
}

func (r *restResolver) getJSON(url, token string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "berrypick-cli")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API %s returned %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decoding response from %s: %w", url, err)
	}
	return nil
}

// apiBase returns the REST API root for the host (api.github.com for github.com,
// the /api/v3 path for enterprise instances).
func apiBase(host string) string {
	if host == "" || host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func trimErr(stderr string, err error) string {
	if s := bytes.TrimSpace([]byte(stderr)); len(s) > 0 {
		return string(s)
	}
	return err.Error()
}
