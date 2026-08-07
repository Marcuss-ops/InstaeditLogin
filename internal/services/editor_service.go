package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// EditorService is the provider-neutral application boundary for editor
// projects. InstaEdit owns the application project and bridge; the injected
// EditorAdapter owns provider-specific editor/runtime calls.
type EditorService interface {
	CreateProject(context.Context, CreateEditorProjectRequest) (*EditorProject, error)
	OpenProject(context.Context, OpenEditorProjectRequest) (*EditorProject, error)
	GetProjectStatus(context.Context, GetEditorProjectStatusRequest) (*EditorProjectStatus, error)
	RequestRender(context.Context, RequestEditorRenderRequest) (*EditorRender, error)
}

// EditorAdapter is the replaceable provider port behind EditorService.
type EditorAdapter interface {
	CreateProject(context.Context, CreateEditorProjectRequest) (*EditorProject, error)
	OpenProject(context.Context, EditorProject) (*EditorProject, error)
	GetProjectStatus(context.Context, EditorProject) (*EditorProjectStatus, error)
	RequestRender(context.Context, EditorProject, RequestEditorRenderRequest) (*EditorRender, error)
}

type EditorProjectBridgeStore interface {
	CreateVeloxProjectBridge(context.Context, *models.VeloxProjectBridge) error
	FindVeloxProjectBridge(context.Context, int64, string) (*models.VeloxProjectBridge, error)
}

// CreateEditorProjectRequest contains only the editor project identity and
// optional editor document. Group/channel/video context is validated by
// InstaEdit's owning records and is deliberately not part of this bridge.
type CreateEditorProjectRequest struct {
	UserID               int64
	WorkspaceID          int64
	ApplicationProjectID string
	ExternalProjectID    string
	Name                 string
	InitialDocument      json.RawMessage
}

type OpenEditorProjectRequest struct {
	UserID               int64
	WorkspaceID          int64
	ApplicationProjectID string
}

type GetEditorProjectStatusRequest struct {
	UserID               int64
	WorkspaceID          int64
	ApplicationProjectID string
}

type RequestEditorRenderRequest struct {
	UserID               int64
	WorkspaceID          int64
	ApplicationProjectID string
	RenderSpec           json.RawMessage
	IdempotencyKey       string
	DeliveryPlan         veloxcontract.DeliveryPlan
	Output               *veloxcontract.JobOutput
}

type EditorProject struct {
	ApplicationProjectID string    `json:"project_id"`
	ExternalProjectID    string    `json:"external_project_id"`
	WorkspaceID          int64     `json:"workspace_id"`
	UserID               int64     `json:"-"`
	Name                 string    `json:"name,omitempty"`
	State                string    `json:"state,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	Created              bool      `json:"-"`
}

type EditorProjectStatus struct {
	ExternalProjectID string    `json:"external_project_id"`
	State             string    `json:"state"`
	LastRenderJobID   string    `json:"last_render_job_id,omitempty"`
	RenderStatus      string    `json:"render_status,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type EditorRender struct {
	JobID             string    `json:"job_id"`
	ExternalProjectID string    `json:"external_project_id"`
	WorkspaceID       int64     `json:"workspace_id"`
	RenderStatus      string    `json:"render_status"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

var (
	ErrEditorServiceNotConfigured = errors.New("editor service is not configured")
	ErrEditorProjectInvalid       = errors.New("editor project is invalid")
	ErrEditorProjectNotFound      = errors.New("editor project not found")
	ErrEditorProjectConflict      = errors.New("editor project already linked to a different external project")
)

type DefaultEditorService struct {
	adapter EditorAdapter
	bridges EditorProjectBridgeStore
	creates singleflight.Group
}

func NewEditorService(adapter EditorAdapter, bridges EditorProjectBridgeStore) *DefaultEditorService {
	return &DefaultEditorService{adapter: adapter, bridges: bridges}
}

var _ EditorService = (*DefaultEditorService)(nil)

func (s *DefaultEditorService) CreateProject(ctx context.Context, req CreateEditorProjectRequest) (*EditorProject, error) {
	if err := validateEditorIdentity(req.UserID, req.WorkspaceID, req.ApplicationProjectID); err != nil {
		return nil, err
	}
	if s == nil || s.adapter == nil || s.bridges == nil {
		return nil, ErrEditorServiceNotConfigured
	}
	key := fmt.Sprintf("%d:%s", req.WorkspaceID, strings.TrimSpace(req.ApplicationProjectID))
	value, err, shared := s.creates.Do(key, func() (any, error) {
		return s.createProjectOnce(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	project, ok := value.(*EditorProject)
	if !ok || project == nil {
		return nil, fmt.Errorf("%w: invalid create result", ErrEditorProjectInvalid)
	}
	if requestedExternalID := strings.TrimSpace(req.ExternalProjectID); requestedExternalID != "" && project.ExternalProjectID != requestedExternalID {
		return nil, ErrEditorProjectConflict
	}
	if shared && project.Created {
		copy := *project
		copy.Created = false
		project = &copy
	}
	return project, nil
}

func (s *DefaultEditorService) createProjectOnce(ctx context.Context, req CreateEditorProjectRequest) (*EditorProject, error) {
	// Re-check inside the singleflight callback. A caller may have joined
	// after an earlier lookup but before the first creator persisted the
	// authoritative mapping.
	if existing, err := s.bridges.FindVeloxProjectBridge(ctx, req.WorkspaceID, strings.TrimSpace(req.ApplicationProjectID)); err != nil {
		return nil, fmt.Errorf("find editor project bridge: %w", err)
	} else if existing != nil {
		return s.projectFromExistingBridge(existing, req)
	}

	project, err := s.adapter.CreateProject(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create editor project: %w", err)
	}
	if err := validateAdapterProject(project, req.WorkspaceID, req.ApplicationProjectID); err != nil {
		return nil, err
	}
	bridge := &models.VeloxProjectBridge{
		ProjectID:         req.ApplicationProjectID,
		WorkspaceID:       req.WorkspaceID,
		ExternalProjectID: project.ExternalProjectID,
		ContractVersion:   models.ProjectBridgeContractVersion,
	}
	if err := bridge.NormalizeAndValidate(); err != nil {
		return nil, fmt.Errorf("validate editor bridge: %w", err)
	}
	if err := s.bridges.CreateVeloxProjectBridge(ctx, bridge); err != nil {
		if existing, findErr := s.bridges.FindVeloxProjectBridge(ctx, req.WorkspaceID, req.ApplicationProjectID); findErr == nil && existing != nil {
			return s.projectFromExistingBridge(existing, req)
		}
		return nil, fmt.Errorf("persist editor project bridge: %w", err)
	}
	project = editorProjectFromBridge(bridge)
	project.UserID = req.UserID
	project.Created = true
	return project, nil
}

func (s *DefaultEditorService) projectFromExistingBridge(existing *models.VeloxProjectBridge, req CreateEditorProjectRequest) (*EditorProject, error) {
	if !bridgeMatchesRequest(existing, req.WorkspaceID, req.ApplicationProjectID) || strings.TrimSpace(existing.ExternalProjectID) == "" {
		return nil, ErrEditorProjectNotFound
	}
	requestedExternalID := strings.TrimSpace(req.ExternalProjectID)
	if requestedExternalID != "" && existing.ExternalProjectID != requestedExternalID {
		return nil, ErrEditorProjectConflict
	}
	project := editorProjectFromBridge(existing)
	project.UserID = req.UserID
	return project, nil
}

func (s *DefaultEditorService) OpenProject(ctx context.Context, req OpenEditorProjectRequest) (*EditorProject, error) {
	bridge, err := s.findBridge(ctx, req.UserID, req.WorkspaceID, req.ApplicationProjectID)
	if err != nil {
		return nil, err
	}
	project := editorProjectFromBridge(bridge)
	project.UserID = req.UserID
	return s.adapter.OpenProject(ctx, *project)
}

func (s *DefaultEditorService) GetProjectStatus(ctx context.Context, req GetEditorProjectStatusRequest) (*EditorProjectStatus, error) {
	bridge, err := s.findBridge(ctx, req.UserID, req.WorkspaceID, req.ApplicationProjectID)
	if err != nil {
		return nil, err
	}
	project := editorProjectFromBridge(bridge)
	project.UserID = req.UserID
	return s.adapter.GetProjectStatus(ctx, *project)
}

func (s *DefaultEditorService) RequestRender(ctx context.Context, req RequestEditorRenderRequest) (*EditorRender, error) {
	if err := validateEditorIdentity(req.UserID, req.WorkspaceID, req.ApplicationProjectID); err != nil {
		return nil, err
	}
	if len(req.RenderSpec) == 0 || !json.Valid(req.RenderSpec) {
		return nil, fmt.Errorf("%w: render_spec must be valid JSON", ErrEditorProjectInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		var err error
		req.IdempotencyKey, err = newEditorIdempotencyKey()
		if err != nil {
			return nil, fmt.Errorf("%w: generate idempotency key: %v", ErrEditorProjectInvalid, err)
		}
	}
	bridge, err := s.findBridge(ctx, req.UserID, req.WorkspaceID, req.ApplicationProjectID)
	if err != nil {
		return nil, err
	}
	project := editorProjectFromBridge(bridge)
	project.UserID = req.UserID
	return s.adapter.RequestRender(ctx, *project, req)
}

func (s *DefaultEditorService) findBridge(ctx context.Context, userID, workspaceID int64, projectID string) (*models.VeloxProjectBridge, error) {
	if err := validateEditorIdentity(userID, workspaceID, projectID); err != nil {
		return nil, err
	}
	if s == nil || s.adapter == nil || s.bridges == nil {
		return nil, ErrEditorServiceNotConfigured
	}
	bridge, err := s.bridges.FindVeloxProjectBridge(ctx, workspaceID, strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("find editor project bridge: %w", err)
	}
	if bridge == nil || !bridgeMatchesRequest(bridge, workspaceID, projectID) || strings.TrimSpace(bridge.ExternalProjectID) == "" {
		return nil, ErrEditorProjectNotFound
	}
	return bridge, nil
}

func bridgeMatchesRequest(bridge *models.VeloxProjectBridge, workspaceID int64, projectID string) bool {
	return bridge != nil &&
		bridge.WorkspaceID == workspaceID &&
		bridge.ProjectID == strings.TrimSpace(projectID)
}

func validateEditorIdentity(userID, workspaceID int64, projectID string) error {
	if userID <= 0 || workspaceID <= 0 || strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("%w: positive user/workspace and project_id are required", ErrEditorProjectInvalid)
	}
	return nil
}

func validateAdapterProject(project *EditorProject, workspaceID int64, applicationProjectID string) error {
	if project == nil || strings.TrimSpace(project.ExternalProjectID) == "" {
		return fmt.Errorf("%w: adapter returned no external project id", ErrEditorProjectInvalid)
	}
	if project.WorkspaceID != 0 && project.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: adapter returned a foreign workspace", ErrEditorProjectInvalid)
	}
	if project.ApplicationProjectID == "" {
		project.ApplicationProjectID = applicationProjectID
	}
	project.WorkspaceID = workspaceID
	return nil
}

func editorProjectFromBridge(bridge *models.VeloxProjectBridge) *EditorProject {
	if bridge == nil {
		return nil
	}
	return &EditorProject{
		ApplicationProjectID: bridge.ProjectID,
		ExternalProjectID:    bridge.ExternalProjectID,
		WorkspaceID:          bridge.WorkspaceID,
		State:                "linked",
		CreatedAt:            bridge.CreatedAt,
	}
}

func newEditorIdempotencyKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "editor-render-" + hex.EncodeToString(raw[:]), nil
}
