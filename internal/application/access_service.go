package application

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/interfaces"
)

type AccessService struct {
	Store   interfaces.ServiceKeyStore
	Compare func([]byte, string) bool
}

func (service AccessService) Bootstrap(ctx context.Context, id string, verifier []byte, role string) error {
	return service.Store.Bootstrap(ctx, id, verifier, role)
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
