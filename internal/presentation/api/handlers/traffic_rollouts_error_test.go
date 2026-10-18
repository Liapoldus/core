package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

func TestTrafficRolloutValidationFailureUsesSafeConfigurationError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "candidate refusal", err: models.ServiceConfigurationCandidateValidationFailed{}, want: "service_config_invalid"},
		{name: "validator unavailable", err: models.ServiceConfigurationUnavailable{Message: "service configuration unavailable"}, want: "service_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			management := config.ManagementWords{}
			management.Codes.ServiceConfigInvalid = "service_config_invalid"
			management.Codes.ServiceUnavailable = "service_unavailable"
			management.Codes.ManagementUnavailable = "management_unavailable"
			got := ""
			deps := ServiceDependencies{
				Management:          management,
				WriteCatalogProblem: func(_ http.ResponseWriter, code, _ string) { got = code },
			}
			response := httptest.NewRecorder()
			writeTrafficRolloutFailure(deps, response, "request", test.err)
			if got != test.want {
				t.Fatalf("traffic rollout error code = %q, want %q", got, test.want)
			}
		})
	}
}
