package storage

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceLinkPolicyView is an immutable copy of the durable caller→target link
// rules keyed by caller instance. Runtime directory assembly never consults the
// durable source; it reads one published view.
type ServiceLinkPolicyView struct {
	rulesByCaller map[string][]models.PeerLinkRule
}

// Rules returns a defensive copy of the published rules for a caller instance.
// A missing caller yields no rules, so the assembly stays deny-by-default.
func (view *ServiceLinkPolicyView) Rules(callerInstanceID string) []models.PeerLinkRule {
	if view == nil {
		return nil
	}
	rules := view.rulesByCaller[callerInstanceID]
	return append([]models.PeerLinkRule(nil), rules...)
}

// ServiceLinkPolicySnapshot publishes a complete, immutable view of Core-owned
// link policy. It is refreshed after every durable mutation and rebuilt from
// SQLite at startup. A refresh failure invalidates the view so a corrupted or
// unreadable policy fails closed instead of serving stale authorization.
type ServiceLinkPolicySnapshot struct {
	reader interfaces.ServiceLinkPolicyReader
	mu     sync.Mutex
	state  atomic.Pointer[ServiceLinkPolicyView]
}

// NewServiceLinkPolicySnapshot builds and immediately publishes the current
// durable policy view.
func NewServiceLinkPolicySnapshot(ctx context.Context, reader interfaces.ServiceLinkPolicyReader) (*ServiceLinkPolicySnapshot, error) {
	if reader == nil {
		return nil, models.PeerLinkPolicyUnavailable{}
	}
	snapshot := &ServiceLinkPolicySnapshot{reader: reader}
	if err := snapshot.Refresh(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Refresh reads durable state only on startup or a mutation path, then swaps
// one pointer. A read failure invalidates the view rather than publishing a
// partial policy that could widen access.
func (snapshot *ServiceLinkPolicySnapshot) Refresh(ctx context.Context) error {
	if snapshot == nil || snapshot.reader == nil {
		return models.PeerLinkPolicyUnavailable{}
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	policies, err := snapshot.reader.List(ctx)
	if err != nil {
		snapshot.state.Store(nil)
		return err
	}
	rulesByCaller := make(map[string][]models.PeerLinkRule, len(policies))
	for _, policy := range policies {
		if policy.Validate() != nil {
			snapshot.state.Store(nil)
			return models.PeerLinkPolicyUnavailable{}
		}
		rulesByCaller[policy.CallerInstanceID] = append(rulesByCaller[policy.CallerInstanceID], policy.Rules...)
	}
	snapshot.state.Store(&ServiceLinkPolicyView{rulesByCaller: rulesByCaller})
	return nil
}

// View returns the currently published immutable view, or nil when no valid
// policy has been read yet.
func (snapshot *ServiceLinkPolicySnapshot) View() *ServiceLinkPolicyView {
	if snapshot == nil {
		return nil
	}
	return snapshot.state.Load()
}
