// Package github resolves the commits belonging to a pull request via the
// GitHub REST API. Credentials are discovered by go-gh's auth package, which
// reads GH_TOKEN/GITHUB_TOKEN from the environment and the token stored by a
// previous `gh auth login` — without requiring the gh binary at runtime. When
// no credentials are found, public repositories are still reachable through
// unauthenticated requests (subject to GitHub's low anonymous rate limit).
package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
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

// NewResolver returns a resolver backed by the GitHub REST API. Token discovery
// is delegated to go-gh, so an existing `gh auth login` or a GH_TOKEN/
// GITHUB_TOKEN environment variable is picked up automatically.
func NewResolver() Resolver {
	return &restResolver{}
}

type restResolver struct{}

func (r *restResolver) PRCommits(pr parse.PRRef) (PRResult, error) {
	host := pr.Host
	if host == "" {
		host = "github.com"
	}
	token, _ := auth.TokenForHost(host)

	fetch, err := newFetcher(host, token)
	if err != nil {
		return PRResult{}, err
	}

	// HEAD SHA from the pull request object.
	base := fmt.Sprintf("repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number)
	var pull struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if _, err := fetch(base, &pull); err != nil {
		return PRResult{}, annotate(err, pr, token != "")
	}

	// Commits, paginated, oldest first (GitHub's documented order). Each page's
	// Link header points at the next one; an empty next URL ends the loop.
	var commits []Commit
	next := base + "/commits?per_page=100"
	for next != "" {
		var batch []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		link, err := fetch(next, &batch)
		if err != nil {
			return PRResult{}, annotate(err, pr, token != "")
		}
		for _, c := range batch {
			commits = append(commits, Commit{SHA: c.SHA, Subject: firstLine(c.Commit.Message)})
		}
		next = link
	}

	res := PRResult{HeadSHA: pull.Head.SHA, Commits: commits}
	if res.HeadSHA == "" && len(commits) > 0 {
		res.HeadSHA = commits[len(commits)-1].SHA
	}
	return res, nil
}

// fetcher performs a GET for path (a relative "repos/..." path or an absolute
// URL taken from a Link header), decodes the JSON body into dst, and returns the
// rel="next" pagination URL (empty when there are no further pages).
type fetcher func(path string, dst any) (next string, err error)

// newFetcher chooses a backend based on whether a token was found. With a token
// it uses go-gh's REST client (handling enterprise hosts, headers and auth);
// without one it issues plain unauthenticated requests so public repos still
// work with zero setup.
func newFetcher(host, token string) (fetcher, error) {
	if token != "" {
		client, err := api.NewRESTClient(api.ClientOptions{
			Host:      host,
			AuthToken: token,
			Headers:   map[string]string{"X-GitHub-Api-Version": "2022-11-28"},
		})
		if err != nil {
			return nil, fmt.Errorf("creating GitHub API client: %w", err)
		}
		return func(path string, dst any) (string, error) {
			// Request returns an *api.HTTPError for non-2xx responses, which
			// annotate inspects for auth failures.
			resp, err := client.Request(http.MethodGet, path, nil)
			if err != nil {
				return "", err
			}
			return decodePage(resp, dst)
		}, nil
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	prefix := apiBase(host) + "/"
	return func(path string, dst any) (string, error) {
		url := path
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = prefix + path
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("building request for %s: %w", url, err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "berrypick-cli")

		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("requesting %s: %w", url, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			// Mirror go-gh's error type so annotate can treat both paths alike.
			return "", &api.HTTPError{StatusCode: resp.StatusCode, Message: resp.Status}
		}
		return decodePage(resp, dst)
	}, nil
}

// decodePage decodes resp into dst and returns the rel="next" link. It always
// closes the body.
func decodePage(resp *http.Response, dst any) (string, error) {
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return "", fmt.Errorf("decoding GitHub response: %w", err)
	}
	return nextLink(resp.Header.Get("Link")), nil
}

// annotate turns an auth-related HTTP failure into actionable guidance. GitHub
// returns 404 (not 403) for private repos accessed without credentials, so an
// unauthenticated 404 is most often "private repo, please authenticate".
func annotate(err error, pr parse.PRRef, authenticated bool) error {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	switch httpErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if !authenticated {
			return fmt.Errorf("cannot access %s PR #%d without credentials (the repository may be private, or you hit GitHub's anonymous rate limit); authenticate with `gh auth login` or set GITHUB_TOKEN, then retry", pr.Slug(), pr.Number)
		}
		return fmt.Errorf("your GitHub credentials don't grant access to %s PR #%d (check the token's repo scope): %s", pr.Slug(), pr.Number, httpErr.Message)
	case http.StatusNotFound:
		if !authenticated {
			return fmt.Errorf("%s PR #%d not found; if the repository is private, authenticate with `gh auth login` or set GITHUB_TOKEN, then retry", pr.Slug(), pr.Number)
		}
	}
	return err
}

// nextLink extracts the rel="next" URL from a GitHub Link header, returning ""
// when there is no next page.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		urlPart := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(urlPart, "<") || !strings.HasSuffix(urlPart, ">") {
			continue
		}
		for _, p := range segs[1:] {
			if strings.TrimSpace(p) == `rel="next"` {
				return urlPart[1 : len(urlPart)-1]
			}
		}
	}
	return ""
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
