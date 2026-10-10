package application

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type AccessService struct {
	Store   interfaces.ServiceKeyStore
	Compare func([]byte, string) bool
}

func (service AccessService) Bootstrap(ctx context.Context, id string, verifier []byte, role string) error {
	return service.Store.Bootstrap(ctx, id, verifier, role)
}

func (service AccessService) Create(ctx context.Context, key models.ServiceKey, record models.AuditRecord) error {
	return service.Store.Create(ctx, key, record)
}

func (service AccessService) Metadata(ctx context.Context) ([]models.ServiceKeyMetadata, error) {
	return service.Store.Metadata(ctx)
}

func (service AccessService) Authenticate(ctx context.Context, token string) (string, bool, error) {
	verifiers, err := service.Store.ActiveVerifiers(ctx)
	if err != nil {
		return "", false, err
	}
	for _, verifier := range verifiers {
		if service.Compare != nil && service.Compare(verifier.Verifier, token) {
			return verifier.ID, true, nil
		}
	}
	return "", false, nil
}
