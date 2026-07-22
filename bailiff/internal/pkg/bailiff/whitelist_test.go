package bailiff

import (
	"context"
	"testing"

	"github.com/google/go-github/v66/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"
	"github.com/stretchr/testify/require"
)

func TestWhitelist(t *testing.T) {
	memberships := map[string][]string{
		"editors": {
			"john",
			"jenny",
		},
		"maintainers": {
			"jenny",
			"max",
		},
	}
	teams := []string{"editors", "maintainers"}

	responses := make([]any, 0)
	for i := 0; i < len(teams); i++ {
		responses = append(responses, []github.User{})
	}
	for _, members := range memberships {
		outMembers := make([]github.User, len(members))
		for i, member := range members {
			outMembers[i] = github.User{
				Login: github.String(member),
			}
		}
		responses = append(responses, outMembers)
	}

	gh := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetOrgsTeamsMembersByOrgByTeamSlug,
			responses...,
		),
	)

	wl := NewTeamWhitelist("ethereum-optimism", teams, github.NewClient(gh))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, wl.sync(ctx))
	require.False(t, wl.Whitelisted("john"))
	require.False(t, wl.Whitelisted("not-a-member"))
	require.NoError(t, wl.sync(ctx))
	for _, team := range memberships {
		for _, member := range team {
			require.True(t, wl.Whitelisted(member))
		}
	}
	require.False(t, wl.Whitelisted("not-a-member"))
}
