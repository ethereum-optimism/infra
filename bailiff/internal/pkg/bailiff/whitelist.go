package bailiff

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/go-github/v66/github"
)

type Whitelister interface {
	Whitelisted(login string) bool
}

type TeamWhitelist struct {
	logins map[string]bool
	teams  []string
	org    string
	github *github.Client
	mtx    sync.RWMutex
}

func NewTeamWhitelist(org string, teams []string, github *github.Client) *TeamWhitelist {
	return &TeamWhitelist{
		org:    org,
		teams:  teams,
		github: github,
	}
}

func (m *TeamWhitelist) Whitelisted(login string) bool {
	m.mtx.RLock()
	defer m.mtx.RUnlock()
	return m.logins[login]
}

func (m *TeamWhitelist) SyncPeriodically(ctx context.Context, lgr log.Logger, interval time.Duration) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		if err := m.sync(ctx); err != nil {
			lgr.Error("failed to sync whitelist", "error", err)
		} else {
			lgr.Info("whitelist synced")
		}

		select {
		case <-tick.C:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *TeamWhitelist) sync(ctx context.Context) error {
	loginsSet := make(map[string]bool)
	for _, team := range m.teams {
		if err := m.syncTeam(ctx, team, loginsSet); err != nil {
			return fmt.Errorf("failed to sync team %s: %w", team, err)
		}
	}

	m.mtx.Lock()
	m.logins = loginsSet
	m.mtx.Unlock()
	return nil
}

func (m *TeamWhitelist) syncTeam(ctx context.Context, team string, loginsSet map[string]bool) error {
	members, _, err := m.github.Teams.ListTeamMembersBySlug(ctx, m.org, team, &github.TeamListTeamMembersOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return fmt.Errorf("failed to list team members: %w", err)
	}

	for _, member := range members {
		loginsSet[member.GetLogin()] = true
	}
	return nil
}
