package storage

import (
	"context"
	"database/sql"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type configurationRolloutQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func serviceConfigurationRolloutBlockers(ctx context.Context, queryer configurationRolloutQueryer, queries ServiceConfigurationSQL, instanceID string) ([]models.ConfigurationBlocker, error) {
	blockers := make([]models.ConfigurationBlocker, 0, 2)
	var open bool
	if err := queryer.QueryRowContext(ctx, queries.queries["configuration-rollout-exists"], instanceID).Scan(&open); err != nil {
		return nil, err
	}
	if open {
		blockers = append(blockers, models.ConfigurationBlockerServiceRollout)
	}
	open = false
	if err := queryer.QueryRowContext(ctx, queries.queries["traffic-rollout-exists"], instanceID).Scan(&open); err != nil {
		return nil, err
	}
	if open {
		blockers = append(blockers, models.ConfigurationBlockerTrafficRollout)
	}
	return blockers, nil
}
