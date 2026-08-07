package services

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type editorBridgeFake struct {
	bridge          *models.VeloxProjectBridge
	created         *models.VeloxProjectBridge
	findCalls       int
	findWorkspaceID int64
	findProjectID   string
}

func (f *editorBridgeFake) CreateVeloxProjectBridge(_ context.Context, bridge *models.VeloxProjectBridge) error {
	f.created = bridge
	f.bridge = bridge
	return nil
}

func (f *editorBridgeFake) FindVeloxProjectBridge(_ context.Context, workspaceID int64, projectID string) (*models.VeloxProjectBridge, error) {
	f.findCalls++
	f.findWorkspaceID, f.findProjectID = workspaceID, projectID
	return f.bridge, nil
}

type editorAdapterFake struct {
	createdRequest CreateEditorProjectRequest
	openedProject  EditorProject
	statusProject  EditorProject
	renderProject  EditorProject
	renderRequest  RequestEditorRenderRequest
	createCalls    int
}

func (f *editorAdapterFake) CreateProject(_ context.Context, req CreateEditorProjectRequest) (*EditorProject, error) {
	f.createCalls++
	f.createdRequest = req
	return &EditorProject{ExternalProjectID: "ve_created", WorkspaceID: req.WorkspaceID}, nil
}

func (f *editorAdapterFake) OpenProject(_ context.Context, project EditorProject) (*EditorProject, error) {
	f.openedProject = project
	return &project, nil
}

func (f *editorAdapterFake) GetProjectStatus(_ context.Context, project EditorProject) (*EditorProjectStatus, error) {
	f.statusProject = project
	return &EditorProjectStatus{ExternalProjectID: project.ExternalProjectID, State: "ready"}, nil
}

func (f *editorAdapterFake) RequestRender(_ context.Context, project EditorProject, req RequestEditorRenderRequest) (*EditorRender, error) {
	f.renderProject = project
	f.renderRequest = req
	return &EditorRender{JobID: "job_render", ExternalProjectID: project.ExternalProjectID, WorkspaceID: project.WorkspaceID}, nil
}

func TestEditorServiceCreateProjectPersistsOnlyBridgeAndIsIdempotent(t *testing.T) {
	adapter := &editorAdapterFake{}
	bridges := &editorBridgeFake{}
	service := NewEditorService(adapter, bridges)
	req := CreateEditorProjectRequest{
		UserID:               11,
		WorkspaceID:          7,
		ApplicationProjectID: "thumb_1",
		Name:                 "Demo",
	}

	first, err := service.CreateProject(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if first.ExternalProjectID != "ve_created" || bridges.created == nil {
		t.Fatalf("first=%+v bridge=%+v", first, bridges.created)
	}
	if adapter.createCalls != 1 {
		t.Fatalf("adapter create calls = %d; want 1", adapter.createCalls)
	}

	second, err := service.CreateProject(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotent CreateProject: %v", err)
	}
	if second.ExternalProjectID != first.ExternalProjectID {
		t.Fatalf("second=%+v first=%+v", second, first)
	}
	if adapter.createCalls != 1 {
		t.Fatalf("adapter create calls after idempotent retry = %d; want 1", adapter.createCalls)
	}
}

func TestEditorServiceConcurrentCreateCallsAdapterOnce(t *testing.T) {
	adapter := &editorAdapterFake{}
	bridges := &editorBridgeFake{}
	service := NewEditorService(adapter, bridges)
	req := CreateEditorProjectRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_concurrent"}

	const callers = 8
	results := make(chan *EditorProject, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			project, err := service.CreateProject(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			results <- project
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent CreateProject: %v", err)
	}
	if adapter.createCalls != 1 {
		t.Fatalf("adapter create calls = %d; want exactly 1", adapter.createCalls)
	}
	if len(results) != callers {
		t.Fatalf("successful results = %d; want %d", len(results), callers)
	}
}

func TestEditorServiceDTOsRejectLegacyOwnershipFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(CreateEditorProjectRequest{}),
		reflect.TypeOf(OpenEditorProjectRequest{}),
		reflect.TypeOf(GetEditorProjectStatusRequest{}),
		reflect.TypeOf(RequestEditorRenderRequest{}),
	} {
		for _, forbidden := range []string{"GroupID", "GroupIDs", "ChannelID", "ChannelIDs", "MemberIDs", "PlatformAccountID", "VideoID", "Language"} {
			if _, ok := typ.FieldByName(forbidden); ok {
				t.Fatalf("%s contains forbidden legacy field %q", typ.Name(), forbidden)
			}
		}
	}
}

func TestEditorServiceRejectsRebindingExistingBridge(t *testing.T) {
	service := NewEditorService(&editorAdapterFake{}, &editorBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID: "thumb_1", ExternalProjectID: "ve_existing", WorkspaceID: 7,
	}})
	_, err := service.CreateProject(context.Background(), CreateEditorProjectRequest{
		UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1", ExternalProjectID: "ve_other",
	})
	if !errors.Is(err, ErrEditorProjectConflict) {
		t.Fatalf("want ErrEditorProjectConflict, got %v", err)
	}
}

func TestEditorServiceDelegatesExistingBridgeThroughProviderNeutralDTO(t *testing.T) {
	adapter := &editorAdapterFake{}
	bridges := &editorBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID:         "thumb_1",
		ExternalProjectID: "ve_existing",
		WorkspaceID:       7,
	}}
	service := NewEditorService(adapter, bridges)

	opened, err := service.OpenProject(context.Background(), OpenEditorProjectRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1"})
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if adapter.openedProject.ExternalProjectID != "ve_existing" || adapter.openedProject.UserID != 11 {
		t.Fatalf("adapter received %+v", adapter.openedProject)
	}
	if opened.ExternalProjectID != "ve_existing" {
		t.Fatalf("opened=%+v", opened)
	}

	status, err := service.GetProjectStatus(context.Background(), GetEditorProjectStatusRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1"})
	if err != nil || status.State != "ready" {
		t.Fatalf("GetProjectStatus: status=%+v err=%v", status, err)
	}

	render, err := service.RequestRender(context.Background(), RequestEditorRenderRequest{
		UserID:               11,
		WorkspaceID:          7,
		ApplicationProjectID: "thumb_1",
		RenderSpec:           json.RawMessage(`{"scenes":[]}`),
	})
	if err != nil {
		t.Fatalf("RequestRender: %v", err)
	}
	if render.JobID != "job_render" || adapter.renderProject.ExternalProjectID != "ve_existing" {
		t.Fatalf("render=%+v project=%+v", render, adapter.renderProject)
	}
	if adapter.renderRequest.IdempotencyKey == "" {
		t.Fatal("service should provide an idempotency key when caller omits it")
	}
}

func TestEditorServiceRejectsBridgeReturnedFromForeignWorkspace(t *testing.T) {
	bridges := &editorBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID: "thumb_1", ExternalProjectID: "ve_existing", WorkspaceID: 99,
	}}
	service := NewEditorService(&editorAdapterFake{}, bridges)
	_, err := service.OpenProject(context.Background(), OpenEditorProjectRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1"})
	if !errors.Is(err, ErrEditorProjectNotFound) {
		t.Fatalf("foreign workspace bridge: want not found, got %v", err)
	}
	if bridges.findWorkspaceID != 7 || bridges.findProjectID != "thumb_1" {
		t.Fatalf("bridge lookup was not scoped: workspace=%d project=%q", bridges.findWorkspaceID, bridges.findProjectID)
	}
}

func TestEditorServiceRejectsBridgeWithoutExternalProjectID(t *testing.T) {
	bridges := &editorBridgeFake{bridge: &models.VeloxProjectBridge{ProjectID: "thumb_1", WorkspaceID: 7}}
	service := NewEditorService(&editorAdapterFake{}, bridges)
	_, err := service.OpenProject(context.Background(), OpenEditorProjectRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1"})
	if !errors.Is(err, ErrEditorProjectNotFound) {
		t.Fatalf("incomplete bridge: want not found, got %v", err)
	}
}

func TestEditorServiceRejectsBridgeReturnedForDifferentProject(t *testing.T) {
	bridges := &editorBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID: "thumb_other", ExternalProjectID: "ve_existing", WorkspaceID: 7,
	}}
	service := NewEditorService(&editorAdapterFake{}, bridges)
	_, err := service.GetProjectStatus(context.Background(), GetEditorProjectStatusRequest{UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1"})
	if !errors.Is(err, ErrEditorProjectNotFound) {
		t.Fatalf("foreign project bridge: want not found, got %v", err)
	}
}

func TestEditorServiceRejectsMissingBridge(t *testing.T) {
	service := NewEditorService(&editorAdapterFake{}, &editorBridgeFake{})
	_, err := service.GetProjectStatus(context.Background(), GetEditorProjectStatusRequest{UserID: 1, WorkspaceID: 2, ApplicationProjectID: "missing"})
	if !errors.Is(err, ErrEditorProjectNotFound) {
		t.Fatalf("err=%v; want ErrEditorProjectNotFound", err)
	}
}

// TestEditorServiceConcurrentDivergentCreateIsConflict: a racing creator
// that wins the unique bridge with a DIFFERENT external project id must
// surface a conflict, not silently adopt the other creator's project.
// conflictingBridgeFake simulates a concurrent creator: the pre-create
// lookup sees no bridge, the insert loses the unique race, and the
// re-read then returns the winning (possibly divergent) bridge.
type conflictingBridgeFake struct {
	bridge    *models.VeloxProjectBridge
	findCalls int
}

func (f *conflictingBridgeFake) CreateVeloxProjectBridge(_ context.Context, _ *models.VeloxProjectBridge) error {
	return errors.New("duplicate")
}

func (f *conflictingBridgeFake) FindVeloxProjectBridge(_ context.Context, _ int64, _ string) (*models.VeloxProjectBridge, error) {
	f.findCalls++
	if f.findCalls == 1 {
		// Pre-create lookup: no bridge yet, so the adapter runs.
		return nil, nil
	}
	// Re-read after the lost insert race: the winner is visible.
	return f.bridge, nil
}

func TestEditorServiceConcurrentDivergentCreateIsConflict(t *testing.T) {
	adapter := &editorAdapterFake{}
	bridges := &conflictingBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID:         "thumb_1",
		ExternalProjectID: "ve_other_creator",
		WorkspaceID:       7,
	}}
	service := NewEditorService(adapter, bridges)

	_, err := service.CreateProject(context.Background(), CreateEditorProjectRequest{
		UserID:               11,
		WorkspaceID:          7,
		ApplicationProjectID: "thumb_1",
		ExternalProjectID:    "ve_this_request",
	})
	if !errors.Is(err, ErrEditorProjectConflict) {
		t.Fatalf("err=%v; want ErrEditorProjectConflict", err)
	}
}

// TestEditorServiceConcurrentEquivalentCreateIsIdempotent: a racing
// creator that won with the SAME external project id is a benign retry.
func TestEditorServiceConcurrentCreateRejectsForeignWinner(t *testing.T) {
	adapter := &editorAdapterFake{}
	bridges := &conflictingBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID: "thumb_other", ExternalProjectID: "ve_created", WorkspaceID: 99,
	}}
	service := NewEditorService(adapter, bridges)

	_, err := service.CreateProject(context.Background(), CreateEditorProjectRequest{
		UserID: 11, WorkspaceID: 7, ApplicationProjectID: "thumb_1",
	})
	if !errors.Is(err, ErrEditorProjectNotFound) {
		t.Fatalf("foreign concurrent winner: want not found, got %v", err)
	}
}

func TestEditorServiceConcurrentEquivalentCreateIsIdempotent(t *testing.T) {
	adapter := &editorAdapterFake{}
	// The adapter fake mints ve_created; the racing winner must have
	// created the same external project for the retry to be benign.
	bridges := &conflictingBridgeFake{bridge: &models.VeloxProjectBridge{
		ProjectID:         "thumb_1",
		ExternalProjectID: "ve_created",
		WorkspaceID:       7,
	}}
	service := NewEditorService(adapter, bridges)

	created, err := service.CreateProject(context.Background(), CreateEditorProjectRequest{
		UserID:               11,
		WorkspaceID:          7,
		ApplicationProjectID: "thumb_1",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.ExternalProjectID != "ve_created" {
		t.Fatalf("created=%+v", created)
	}
}
