package github

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/fverse/berrypick/internal/parse"
)

func TestNextLink(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{
			"next and last",
			`<https://api.github.com/repositories/1/pulls/2/commits?page=2>; rel="next", <https://api.github.com/repositories/1/pulls/2/commits?page=5>; rel="last"`,
			"https://api.github.com/repositories/1/pulls/2/commits?page=2",
		},
		{
			"only prev and first (no next)",
			`<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=1>; rel="first"`,
			"",
		},
		{
			"next not first in list",
			`<https://api.github.com/x?page=1>; rel="first", <https://api.github.com/x?page=3>; rel="next"`,
			"https://api.github.com/x?page=3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextLink(tc.header); got != tc.want {
				t.Errorf("nextLink() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnnotate(t *testing.T) {
	pr := parse.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 7}

	t.Run("unauthenticated 404 suggests auth", func(t *testing.T) {
		err := annotate(&api.HTTPError{StatusCode: http.StatusNotFound}, pr, false)
		if !strings.Contains(err.Error(), "gh auth login") || !strings.Contains(err.Error(), "private") {
			t.Errorf("got %q, want guidance about private repo and gh auth login", err)
		}
	})

	t.Run("unauthenticated 403 suggests auth", func(t *testing.T) {
		err := annotate(&api.HTTPError{StatusCode: http.StatusForbidden}, pr, false)
		if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
			t.Errorf("got %q, want guidance mentioning GITHUB_TOKEN", err)
		}
	})

	t.Run("authenticated 403 reports scope", func(t *testing.T) {
		err := annotate(&api.HTTPError{StatusCode: http.StatusForbidden, Message: "denied"}, pr, true)
		if !strings.Contains(err.Error(), "scope") {
			t.Errorf("got %q, want guidance about token scope", err)
		}
	})

	t.Run("authenticated 404 passes through unchanged", func(t *testing.T) {
		in := &api.HTTPError{StatusCode: http.StatusNotFound}
		if got := annotate(in, pr, true); !errors.Is(got, error(in)) {
			t.Errorf("got %q, want original error returned unchanged", got)
		}
	})

	t.Run("non-HTTP error passes through", func(t *testing.T) {
		in := errors.New("boom")
		if got := annotate(in, pr, false); got != in {
			t.Errorf("got %q, want original error", got)
		}
	})
}
