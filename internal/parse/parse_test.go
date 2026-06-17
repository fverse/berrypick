package parse

import "testing"

func TestParseCommitHash(t *testing.T) {
	cases := []string{
		"a1b2c3d4",
		"a1b2c3d4e5f6",
		"A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2", // full 40, mixed case
		"abcd", // minimum abbreviation
	}
	for _, in := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", in, err)
			continue
		}
		if got.Kind != KindCommit {
			t.Errorf("Parse(%q) Kind = %v, want KindCommit", in, got.Kind)
		}
		if got.Commit != lower(in) {
			t.Errorf("Parse(%q) Commit = %q, want lowercased input", in, got.Commit)
		}
	}
}

func lower(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'A' && b <= 'F' {
			out[i] = b + ('a' - 'A')
		}
	}
	return string(out)
}

func TestParsePRURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want PRRef
	}{
		{
			name: "canonical",
			in:   "https://github.com/owner/repo/pull/123",
			want: PRRef{Host: "github.com", Owner: "owner", Repo: "repo", Number: 123},
		},
		{
			name: "trailing slash",
			in:   "https://github.com/owner/repo/pull/7/",
			want: PRRef{Host: "github.com", Owner: "owner", Repo: "repo", Number: 7},
		},
		{
			name: "scheme-less",
			in:   "github.com/acme/widgets/pull/42",
			want: PRRef{Host: "github.com", Owner: "acme", Repo: "widgets", Number: 42},
		},
		{
			name: "enterprise host",
			in:   "https://ghe.example.com/team/svc/pull/9",
			want: PRRef{Host: "ghe.example.com", Owner: "team", Repo: "svc", Number: 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if got.Kind != KindPullRequest {
				t.Fatalf("Parse(%q) Kind = %v, want KindPullRequest", tc.in, got.Kind)
			}
			if got.PR != tc.want {
				t.Errorf("Parse(%q) PR = %+v, want %+v", tc.in, got.PR, tc.want)
			}
		})
	}
}

func TestParseBlame(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want BlameRef
	}{
		{"nested path", "internal/git/run.go:42", BlameRef{File: "internal/git/run.go", Line: 42}},
		{"bare file", "Makefile:10", BlameRef{File: "Makefile", Line: 10}},
		{"line one", "main.go:1", BlameRef{File: "main.go", Line: 1}},
		{"path with spaces", "my file.txt:7", BlameRef{File: "my file.txt", Line: 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if got.Kind != KindBlame {
				t.Fatalf("Parse(%q) Kind = %v, want KindBlame", tc.in, got.Kind)
			}
			if got.Blame != tc.want {
				t.Errorf("Parse(%q) Blame = %+v, want %+v", tc.in, got.Blame, tc.want)
			}
		})
	}
}

func TestParseBlameInvalid(t *testing.T) {
	// Zero/negative line numbers must not be accepted as blame references.
	for _, in := range []string{"foo.go:0", "foo.go:-1"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", in)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"xyz",                                   // non-hex
		"abc",                                   // too short (< 4)
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3", // too long (> 40)
		"https://github.com/owner/repo/issues/1",     // not a PR path
		"https://github.com/owner/repo/pull/abc",     // non-numeric PR number
		"https://github.com/owner/repo/pull/0",       // non-positive PR number
		"https://github.com/owner/repo",              // missing pull segment
	}
	for _, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", in)
		}
	}
}

func TestBranchName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", "cherry-pick/a1b2c3d4"},
		{"A1B2C3D4", "cherry-pick/a1b2c3d4"},
		{"abc123", "cherry-pick/abc123"}, // shorter than 8 left intact
	}
	for _, tc := range cases {
		if got := BranchName(tc.in); got != tc.want {
			t.Errorf("BranchName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRemoteURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Repo
	}{
		{"https", "https://github.com/owner/repo.git", Repo{Host: "github.com", Owner: "owner", Name: "repo"}},
		{"https no .git", "https://github.com/owner/repo", Repo{Host: "github.com", Owner: "owner", Name: "repo"}},
		{"scp-like", "git@github.com:owner/repo.git", Repo{Host: "github.com", Owner: "owner", Name: "repo"}},
		{"ssh url", "ssh://git@github.com/owner/repo.git", Repo{Host: "github.com", Owner: "owner", Name: "repo"}},
		{"enterprise scp", "git@ghe.example.com:team/svc.git", Repo{Host: "ghe.example.com", Owner: "team", Name: "svc"}},
		{"nested group", "https://gitlab.example.com/group/sub/repo.git", Repo{Host: "gitlab.example.com", Owner: "group/sub", Name: "repo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RemoteURL(tc.in)
			if err != nil {
				t.Fatalf("RemoteURL(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("RemoteURL(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRemoteURLInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "not-a-url", "https://github.com/owner"} {
		if _, err := RemoteURL(in); err == nil {
			t.Errorf("RemoteURL(%q) expected error, got nil", in)
		}
	}
}

func TestNewPRURL(t *testing.T) {
	r := Repo{Host: "github.com", Owner: "owner", Name: "repo"}
	got := r.NewPRURL("cherry-pick/98f40d37")
	want := "https://github.com/owner/repo/pull/new/cherry-pick/98f40d37"
	if got != want {
		t.Errorf("NewPRURL = %q, want %q", got, want)
	}
}

func TestSlug(t *testing.T) {
	p := PRRef{Owner: "owner", Repo: "repo"}
	if got := p.Slug(); got != "owner/repo" {
		t.Errorf("Slug() = %q, want owner/repo", got)
	}
}
