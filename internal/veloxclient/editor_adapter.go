package veloxclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// VeloxAdapter is the provider-specific implementation of the
// provider-neutral services.EditorAdapter port. It exposes no Velox types to
// the application service: only opaque project references and render results
// cross the boundary.
type VeloxAdapter struct {
	client *Client
}

// NewVeloxAdapter constructs the Velox implementation. A nil client returns
// nil so bootstrap can fail closed when Velox is not configured.
func NewVeloxAdapter(client *Client) *VeloxAdapter {
	if client == nil {
		return nil
	}
	return &VeloxAdapter{client: client}
}

var _ services.EditorAdapter = (*VeloxAdapter)(nil)

type editorContextResponse struct {
	ProjectID   string `json:"project_id"`
	WorkspaceID int64  `json:"workspace_id"`
}

func (a *VeloxAdapter) CreateProject(ctx context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
	if a == nil || a.client == nil {
		return nil, services.ErrEditorServiceNotConfigured
	}
	externalID := strings.TrimSpace(req.ExternalProjectID)
	if externalID == "" {
		externalID = newVeloxProjectID(req.WorkspaceID, req.ApplicationProjectID)
	}
	if err := validateVeloxProjectID(externalID); err != nil {
		return nil, fmt.Errorf("%w: %v", services.ErrEditorProjectInvalid, err)
	}

	project := &services.EditorProject{
		ApplicationProjectID: req.ApplicationProjectID,
		ExternalProjectID:    externalID,
		WorkspaceID:          req.WorkspaceID,
		Name:                 req.Name,
		State:                "ready",
		CreatedAt:            time.Now().UTC(),
	}
	// The current Velox control plane lazily materializes editor projects on
	// their first document write. This keeps Velox from owning a project
	// catalog while still ensuring a newly-created bridge has its initial
	// editor state persisted before the launcher is returned.
	if len(req.InitialDocument) > 0 {
		if !json.Valid(req.InitialDocument) {
			return nil, fmt.Errorf("%w: initial document must be valid JSON", services.ErrEditorProjectInvalid)
		}
		if err := a.putDocument(ctx, req.UserID, req.WorkspaceID, externalID, req.InitialDocument); err != nil {
			return nil, fmt.Errorf("initialize Velox project: %w", err)
		}
	}
	return project, nil
}

func (a *VeloxAdapter) OpenProject(ctx context.Context, project services.EditorProject) (*services.EditorProject, error) {
	if err := a.validateProject(project); err != nil {
		return nil, err
	}
	var response editorContextResponse
	if err := a.proxyJSON(ctx, http.MethodGet, project.UserID, project.WorkspaceID, project.ExternalProjectID, "", nil, &response); err != nil {
		if errors.Is(err, veloxapi.ErrNotFound) {
			return nil, services.ErrEditorProjectNotFound
		}
		return nil, fmt.Errorf("open Velox project: %w", err)
	}
	if response.ProjectID != "" && response.ProjectID != project.ExternalProjectID {
		return nil, fmt.Errorf("%w: Velox returned a different project", services.ErrEditorProjectInvalid)
	}
	if response.WorkspaceID != 0 && response.WorkspaceID != project.WorkspaceID {
		return nil, fmt.Errorf("%w: Velox echoed a foreign workspace", services.ErrEditorProjectInvalid)
	}
	project.State = "ready"
	return &project, nil
}

func (a *VeloxAdapter) GetProjectStatus(ctx context.Context, project services.EditorProject) (*services.EditorProjectStatus, error) {
	if err := a.validateProject(project); err != nil {
		return nil, err
	}
	var response editorContextResponse
	if err := a.proxyJSON(ctx, http.MethodGet, project.UserID, project.WorkspaceID, project.ExternalProjectID, "", nil, &response); err != nil {
		if errors.Is(err, veloxapi.ErrNotFound) {
			return nil, services.ErrEditorProjectNotFound
		}
		return nil, fmt.Errorf("get Velox project status: %w", err)
	}
	if response.ProjectID != "" && response.ProjectID != project.ExternalProjectID {
		return nil, fmt.Errorf("%w: Velox returned a different project", services.ErrEditorProjectInvalid)
	}
	if response.WorkspaceID != 0 && response.WorkspaceID != project.WorkspaceID {
		return nil, fmt.Errorf("%w: Velox echoed a foreign workspace", services.ErrEditorProjectInvalid)
	}
	return &services.EditorProjectStatus{
		ExternalProjectID: project.ExternalProjectID,
		State:             "ready",
		UpdatedAt:         time.Now().UTC(),
	}, nil
}

func (a *VeloxAdapter) RequestRender(ctx context.Context, project services.EditorProject, req services.RequestEditorRenderRequest) (*services.EditorRender, error) {
	if err := a.validateProject(project); err != nil {
		return nil, err
	}
	if len(req.RenderSpec) == 0 || !json.Valid(req.RenderSpec) {
		return nil, fmt.Errorf("%w: render_spec must be valid JSON", services.ErrEditorProjectInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: idempotency key is required", services.ErrEditorProjectInvalid)
	}

	job, err := a.client.CreateJob(ctx, project.WorkspaceID, project.UserID, veloxapi.CreateJobRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  req.IdempotencyKey,
		JobType:         "editor.render",
		TemplateID:      "editor.project",
		TemplateVersion: 1,
		VideoName:       project.ExternalProjectID,
		Spec:            req.RenderSpec,
		Output:          req.Output,
		ProjectID:       project.ExternalProjectID,
		RenderSpec:      req.RenderSpec,
		DeliveryPlan:    req.DeliveryPlan,
		RenderOnly:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("request Velox render: %w", err)
	}
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return nil, fmt.Errorf("%w: Velox returned no render job", services.ErrEditorProjectInvalid)
	}
	return &services.EditorRender{
		JobID:             job.ID,
		ExternalProjectID: project.ExternalProjectID,
		WorkspaceID:       project.WorkspaceID,
		RenderStatus:      job.RenderStatus,
		CreatedAt:         job.CreatedAt,
		UpdatedAt:         job.UpdatedAt,
	}, nil
}

func (a *VeloxAdapter) putDocument(ctx context.Context, userID, workspaceID int64, projectID string, document json.RawMessage) error {
	return a.proxyJSON(ctx, http.MethodPut, userID, workspaceID, projectID, "/document", bytes.NewReader(document), nil)
}

func (a *VeloxAdapter) proxyJSON(ctx context.Context, method string, userID, workspaceID int64, projectID, suffix string, body io.Reader, out any) error {
	path := "/projects/" + url.PathEscape(projectID) + suffix
	resp, err := a.client.ProxyForProject(ctx, method, path, userID, workspaceID, projectID, body, "application/json", editorScope(method))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return veloxapi.ErrNotFound
	}
	if resp.StatusCode == http.StatusForbidden {
		return veloxapi.ErrWorkspaceMismatch
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Velox editor returned HTTP %d", resp.StatusCode)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Velox editor response: %w", err)
	}
	return nil
}

func editorScope(method string) []string {
	if method == http.MethodGet || method == http.MethodHead {
		return []string{ScopeVeloxEditorRead}
	}
	return []string{ScopeVeloxEditorWrite}
}

func (a *VeloxAdapter) validateProject(project services.EditorProject) error {
	if a == nil || a.client == nil {
		return services.ErrEditorServiceNotConfigured
	}
	if project.UserID <= 0 || project.WorkspaceID <= 0 {
		return fmt.Errorf("%w: project identity is invalid", services.ErrEditorProjectInvalid)
	}
	if err := validateVeloxProjectID(project.ExternalProjectID); err != nil {
		return fmt.Errorf("%w: %v", services.ErrEditorProjectInvalid, err)
	}
	return nil
}

func validateVeloxProjectID(projectID string) error {
	if len(projectID) < 4 || len(projectID) > 128 || (!strings.HasPrefix(projectID, "ve_") && !strings.HasPrefix(projectID, "vx_")) || strings.ContainsAny(projectID, "\r\n") {
		return fmt.Errorf("external project id must use a supported opaque handle format")
	}
	return nil
}

// newVeloxProjectID derives the provider handle from the authoritative
// InstaEdit identity. The Velox editor endpoint materializes projects lazily
// on PUT /document, so the same application project must address the same
// opaque provider path across API replicas and retries.
func newVeloxProjectID(workspaceID int64, applicationProjectID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("instaedit-editor:%d:%s", workspaceID, strings.TrimSpace(applicationProjectID))))
	return "ve_" + hex.EncodeToString(sum[:16])
}
