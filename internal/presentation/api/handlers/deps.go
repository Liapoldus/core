package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

type Dependencies struct {
	AccessService       *application.AccessService
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	GenerateServiceKey  func(int, int) (string, string, []byte, error)
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}

type PluginDependencies struct {
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	PluginIDField       string
	Plugins             []any
	AdminLimits         AdminLimits
	Operations          application.OperationService
	PluginLinks         *application.PluginLinkPolicyService
	TrafficRollouts     *application.TrafficRolloutService
	TrafficRolloutAPI   config.TrafficRolloutAPIContract
	ListAdminSurfaces   func(context.Context) ([]AdminSurfaceItem, error)
	AdminSurfaceDigest  func(context.Context, string) (string, error)
	DispatchAdmin       func(context.Context, AdminInvocation, []byte) (PluginAdminResult, error)
	ForwardArtifact     func(context.Context, AdminInvocation, []byte, string, io.ReadCloser) (PluginAdminResult, error)
	RecordAdminOutcome  func(context.Context, string, string, string, bool) error
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
	WritePage           func(http.ResponseWriter, any, *http.Request, string)
}

type PluginAdminResult struct {
	Status      int
	ContentType string
	Body        []byte
}

type AdminSurfaceItem struct {
	InstanceID string          `json:"instanceId"`
	Descriptor json.RawMessage `json:"descriptor"`
	SHA256     string          `json:"sha256"`
}

type AdminInvocation struct {
	CallerID       string
	InstanceID     string
	PageID         string
	ActionID       string
	SurfaceDigest  string
	RequestID      string
	IdempotencyKey string
	IfMatch        string
}

type AdminLimits struct {
	JSONRequestBytes   int64
	ArtifactBytes      int64
	MinimumArtifact    int64
	MetadataBytes      int64
	MultipartBytes     int64
	MaximumRequest     int64
	MetadataPartName   string
	ArtifactPartName   string
	MultipartMediaType string
	MetadataMediaType  string
}

type ManagementDependencies struct {
	Audit               *application.AuditService
	Operations          application.OperationService
	DataPlaneState      string
	DataPlaneReason     string
	DataPlaneReadiness  func(context.Context) (string, string)
	DataPlaneDrift      func(context.Context) bool
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}
