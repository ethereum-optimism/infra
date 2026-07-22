package bailiff

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/go-github/v66/github"
	"github.com/gorilla/mux"
	"github.com/migueleliasweb/go-github-mock/src/mock"
	"github.com/stretchr/testify/require"
)

type mockRepusher struct {
	called bool
	rc     repusherCall
}

type repusherCall struct {
	forkRepo       string
	srcBranch      string
	upstreamBranch string
	requestedSHA   string
}

func (m *mockRepusher) Repush(ctx context.Context, forkRepo, srcBranch, upstreamBranch, requestedSHA string) error {
	m.called = true
	m.rc.forkRepo = forkRepo
	m.rc.srcBranch = srcBranch
	m.rc.upstreamBranch = upstreamBranch
	m.rc.requestedSHA = requestedSHA
	return nil
}

type mockWhitelist struct {
	people map[string]bool
}

func (m *mockWhitelist) Whitelisted(login string) bool {
	return m.people[login]
}

func TestFormatRepushBranch(t *testing.T) {
	tests := []struct {
		name string
		repo string
		in   string
		out  string
	}{
		{"basic", "foo/optimism", "master", "external-fork/0aeca8bc7690cfe752f8b495980e5606e84b607a445e8a53552072566ddd7478"},
		{"different repo", "foo/otherrepo", "master", "external-fork/10c55da1bd272ed42ca341d89501741aa027d56ae6be51bca669bed4372a00cc"},
		{"slashes", "foouser/optimism", "branch/with/slashes", "external-fork/d884bc21bd02f15d28349e1dfdd0dd5ad04a875084e1b77d931379a5e8319cc4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.out, FormatRepushBranch(tt.repo, tt.in))
		})
	}
}

func TestEventHandler_OnIssueComment(t *testing.T) {
	prs := map[string]github.PullRequest{
		"1": {
			State: github.String("closed"),
		},
		"2": {
			State: github.String("open"),
			Head: &github.PullRequestBranch{
				Repo: &github.Repository{
					FullName: github.String("ethereum-optimism/optimism"),
				},
			},
		},
		"3": {
			State: github.String("open"),
			Head: &github.PullRequestBranch{
				Repo: &github.Repository{
					FullName: github.String("not-ethereum-optimism/optimism"),
				},
				SHA: github.String("aaaaaaaa"),
				Ref: github.String("feat/super-branch"),
			},
		},
	}
	m := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				vars := mux.Vars(r)
				pr, ok := prs[vars["pull_number"]]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}

				w.Write(mock.MustMarshal(pr))
			}),
		),
		mock.WithRequestMatch(
			mock.PostReposStatusesByOwnerByRepoBySha,
			github.CheckRun{},
		),
	)
	gh := github.NewClient(m)

	whitelist := &mockWhitelist{
		people: map[string]bool{
			"whitelisted": true,
		},
	}

	repusher := new(mockRepusher)
	hdlr := &EventHandler{
		lgr: log.New(log.DiscardHandler()),
		gh:  gh,
		config: &Config{
			Org:            "ethereum-optimism",
			Repo:           "optimism",
			TriggerPattern: regexp.MustCompile(`^/ci authorize (?P<sha>[a-f0-9]+)$`),
		},
		repusher:  repusher,
		whitelist: whitelist,
	}

	ignoredTests := []struct {
		name string
		ev   *github.IssueCommentEvent
		err  error
	}{
		{
			"not a PR",
			&github.IssueCommentEvent{
				Issue: &github.Issue{
					PullRequestLinks: nil,
				},
			},
			ErrNotPullRequest,
		},
		{
			"not a creation event",
			&github.IssueCommentEvent{
				Action: github.String("edited"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
				},
			},
			ErrNotCreation,
		},
		{
			"bad pull request",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(0),
				},
			},
			ErrPRNotFound,
		},
		{
			"pull request is closed",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(1),
				},
			},
			ErrPRNotOpen,
		},
		{
			"pull request is on the upstream repo",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(2),
				},
			},
			ErrPRFromUpstream,
		},
		{
			"comment is from someone who isn't whitelisted",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Sender: &github.User{
					Login: github.String("not-whitelisted"),
				},
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(3),
				},
			},
			ErrNonWhitelisted,
		},
		{
			"comment is too long",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Sender: &github.User{
					Login: github.String("whitelisted"),
				},
				Comment: &github.IssueComment{
					Body: github.String(strings.Repeat("a", MaxCommentLen+1)),
				},
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(3),
				},
			},
			ErrCommentTooLong,
		},
		{
			"comment doesn't match trigger pattern",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Sender: &github.User{
					Login: github.String("whitelisted"),
				},
				Comment: &github.IssueComment{
					Body: github.String("no trigger pattern here"),
				},
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(3),
				},
			},
			ErrNoTriggerPattern,
		},
		{
			"comment has mismatched SHA",
			&github.IssueCommentEvent{
				Action: github.String("created"),
				Sender: &github.User{
					Login: github.String("whitelisted"),
				},
				Comment: &github.IssueComment{
					Body: github.String("/ci authorize 12345678"),
				},
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{},
					Number:           github.Int(3),
				},
			},
			ErrMismatchedSHA,
		},
	}
	for _, tt := range ignoredTests {
		t.Run(fmt.Sprintf("ignored/%s", tt.name), func(t *testing.T) {
			err := hdlr.OnIssueComment(context.Background(), tt.ev)
			require.Equal(t, tt.err, err)
			require.False(t, repusher.called)
		})
	}

	t.Run("successful repush", func(t *testing.T) {
		err := hdlr.OnIssueComment(context.Background(), &github.IssueCommentEvent{
			Action: github.String("created"),
			Sender: &github.User{
				Login: github.String("whitelisted"),
			},
			Comment: &github.IssueComment{
				Body: github.String("/ci authorize aaaaaaaa"),
			},
			Issue: &github.Issue{
				PullRequestLinks: &github.PullRequestLinks{},
				Number:           github.Int(3),
			},
		})
		require.NoError(t, err)
		require.True(t, repusher.called)
		require.EqualValues(t, repusherCall{
			forkRepo:       "not-ethereum-optimism/optimism",
			srcBranch:      "feat/super-branch",
			requestedSHA:   "aaaaaaaa",
			upstreamBranch: "external-fork/3593dababb1188e36163c6b679d9e382371b697de298374183ac5457082c334d",
		}, repusher.rc)
	})
}

func TestFindCommentSHA(t *testing.T) {
	pattern := regexp.MustCompile(`(?m)^/ci authorize (?P<sha>[a-f0-9]+)$`)

	tests := []struct {
		name     string
		comment  string
		expected string
	}{
		{
			"basic",
			"/ci authorize 12345678",
			"12345678",
		},
		{
			"no match",
			"no match here",
			"",
		},
		{
			"multiline with multiple matches",
			"some commentary here or whatever.\n/ci authorize 12345678\nmore commentary\n/ci authorize abcd",
			"12345678",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, FindCommentSHA(pattern, tt.comment))
		})
	}
}
