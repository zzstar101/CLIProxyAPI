package cliproxy

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// refreshCommandCodeAccounts runs after the listener starts. Discovery never
// delays startup and a transient failure preserves the last complete snapshot.
func (s *Service) refreshCommandCodeAccounts(ctx context.Context) {
	refresh := func() {
		for _, auth := range s.coreManager.List() {
			if ctx.Err() != nil {
				return
			}
			if auth.Provider != commandcode.Provider || auth.Disabled {
				continue
			}
			s.cfgMu.RLock()
			cfg := s.cfg
			s.cfgMu.RUnlock()
			key := commandcode.APIKey(auth)
			client := commandcode.NewClient(helps.NewProxyAwareHTTPClient(ctx, cfg, auth, 0), "", key)
			discoveryStarted := time.Now().UTC()
			account, err := client.Account(ctx)
			var models []commandcode.Model
			if err != nil {
				var apiErr *commandcode.APIError
				definitive := errors.Is(err, commandcode.ErrUnsupportedPlan) || errors.Is(err, commandcode.ErrInactiveSubscription) || (errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403))
				previous, hasSnapshot := commandcode.ReadSnapshot(auth)
				if !definitive || !hasSnapshot {
					if hasSnapshot {
						previous.LastError = err.Error()
						s.commitCommandCodeDiscovery(ctx, auth.ID, key, previous)
					}
					log.WithError(err).Debug("command-code: account discovery failed; keeping previous snapshot")
					continue
				}
				if account.ID == "" {
					account = previous.Account
					account.Subscription.Status = "unauthorized"
				}
				models = previous.Models
			} else {
				models, err = client.Models(ctx)
				if err != nil {
					if previous, ok := commandcode.ReadSnapshot(auth); ok {
						previous.LastError = err.Error()
						s.commitCommandCodeDiscovery(ctx, auth.ID, key, previous)
					}
					log.WithError(err).Debug("command-code: catalog discovery failed; keeping previous snapshot")
					continue
				}
			}
			next := commandcode.Snapshot{Account: account, Models: models, FetchedAt: discoveryStarted}
			if err != nil {
				previous, _ := commandcode.ReadSnapshot(auth)
				next.FetchedAt = previous.FetchedAt
				next.LastError = err.Error()
			}
			s.commitCommandCodeDiscovery(ctx, auth.ID, key, next)
		}
	}
	refresh()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
func (s *Service) commitCommandCodeDiscovery(ctx context.Context, id, key string, next commandcode.Snapshot) {
	unlock := commandcode.LockAccount(id)
	defer unlock()
	if ctx.Err() != nil {
		return
	}
	latest, ok := s.coreManager.GetByID(id)
	if !ok || latest.Disabled || latest.Provider != commandcode.Provider || commandcode.APIKey(latest) != key {
		return
	}
	previous, ok := commandcode.ReadSnapshot(latest)
	if ok && previous.Account.ID != next.Account.ID {
		return
	}
	// A newer manual refresh must not be overwritten by an older in-flight poll.
	if previous.FetchedAt.After(next.FetchedAt) {
		return
	}
	updated := latest.Clone()
	commandcode.SetSnapshot(updated, next)
	// Runtime quota polling must not rewrite credentials or reset cooldowns.
	if _, err := s.coreManager.Update(coreauth.WithSkipPersist(ctx), updated); err != nil {
		return
	}
	overrides := commandcode.Overrides(updated)
	if !reflect.DeepEqual(commandCodeEnabledIDs(previous, overrides), commandCodeEnabledIDs(next, overrides)) {
		s.registerModelsForAuth(ctx, updated)
		s.coreManager.RefreshSchedulerEntry(updated.ID)
	}
}

func commandCodeEnabledIDs(snapshot commandcode.Snapshot, overrides map[string]bool) map[string]int {
	result := make(map[string]int)
	for _, model := range snapshot.Models {
		if commandcode.Enabled(snapshot.Account, model, overrides) {
			result[model.ID] = model.ContextLength
		}
	}
	return result
}
