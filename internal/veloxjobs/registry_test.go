package veloxjobs

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

type testValidator struct{ err error }

func (v testValidator) Validate(json.RawMessage) error { return v.err }

type testCompiler struct{}

func (testCompiler) Compile(spec json.RawMessage) (CompiledSpec, error) {
	return CompiledSpec{JobType: "custom.v1", Spec: spec, OperationCount: 7}, nil
}

type testEstimator struct{}

func (testEstimator) Estimate(json.RawMessage, *veloxcontract.JobOutput) (CostEstimate, error) {
	return CostEstimate{RenderUnits: 9}, nil
}

func TestRegistryRegisterResolve(t *testing.T) {
	r := NewRegistry()
	def := Definition{
		JobType:       "custom.v1",
		Validator:     testValidator{},
		Compiler:      testCompiler{},
		CostEstimator: testEstimator{},
	}
	if err := r.Register(def); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Resolve(" custom.v1 ")
	if err != nil || got.JobType != "custom.v1" {
		t.Fatalf("Resolve = %+v, %v", got, err)
	}
	compiled, err := got.Compiler.Compile(json.RawMessage(`{"x":1}`))
	if err != nil || compiled.OperationCount != 7 {
		t.Fatalf("Compile = %+v, %v", compiled, err)
	}
}

func TestRegistryRejectsIncompleteDefinition(t *testing.T) {
	if err := NewRegistry().Register(Definition{JobType: "missing.v1"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Register error = %v, want ErrInvalidDefinition", err)
	}
}

func TestRegistryUnknownJobType(t *testing.T) {
	_, err := NewDefaultRegistry().Resolve("unknown.v1")
	if !errors.Is(err, ErrUnknownJobType) {
		t.Fatalf("Resolve error = %v, want ErrUnknownJobType", err)
	}
}

func TestDefaultRegistryNamesStable(t *testing.T) {
	got := NewDefaultRegistry().Names()
	want := []string{"audio.mux.v1", "clip.stock.v1", "microcut.batch.v1", "scene.composite.v1", "scene.image.v1", "slideshow.v1"}
	if len(got) != len(want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names = %v, want %v", got, want)
		}
	}
}

func TestSceneCompositeDefinitionValidatesCompilesAndEstimates(t *testing.T) {
	def, err := NewDefaultRegistry().Resolve("scene.composite.v1")
	if err != nil {
		t.Fatal(err)
	}
	spec := json.RawMessage(`{"scenes":[{"id":"one"},{"id":"two"}]}`)
	if err := def.Validator.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	compiled, err := def.Compiler.Compile(spec)
	if err != nil || compiled.OperationCount != 2 {
		t.Fatalf("Compile = %+v, %v", compiled, err)
	}
	estimate, err := def.CostEstimator.Estimate(spec, &veloxcontract.JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"})
	if err != nil || estimate.RenderUnits != 4 || estimate.SceneCount != 2 {
		t.Fatalf("Estimate = %+v, %v", estimate, err)
	}
}

func TestDefaultRegistryRejectsMissingTechnicalArray(t *testing.T) {
	def, err := NewDefaultRegistry().Resolve("slideshow.v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Validator.Validate(json.RawMessage(`{"scenes":[]}`)); err == nil {
		t.Fatal("expected slideshow validator to require images")
	}
}
