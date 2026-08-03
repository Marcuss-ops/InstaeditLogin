// Package veloxjobs contains the central technical job-type registry for
// the velox.job.v1 submission contract.
//
// Editorial templates are deliberately not registered here: they remain
// template_id/spec data. This registry only owns technical differences in
// validation, compilation and resource estimation.
package veloxjobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

var (
	// ErrUnknownJobType is returned when a canonical submission names a
	// technical job type that has not been registered.
	ErrUnknownJobType = errors.New("unknown velox job_type")
	// ErrInvalidDefinition is returned when an incomplete definition is
	// registered. Failing at construction prevents a half-working registry.
	ErrInvalidDefinition = errors.New("invalid velox job definition")
)

// Validator validates the job-type-specific spec. Envelope validation stays
// in veloxcontract.JobSubmissionRequest.ValidateCanonical.
type Validator interface {
	Validate(spec json.RawMessage) error
}

// Compiler normalizes a job spec into the representation sent to the worker.
// A compiler is intentionally independent of HTTP and storage.
type Compiler interface {
	Compile(spec json.RawMessage) (CompiledSpec, error)
}

// CostEstimator estimates deterministic technical resource usage. Estimates
// are advisory and do not change acceptance of an otherwise valid job.
type CostEstimator interface {
	Estimate(spec json.RawMessage, output *veloxcontract.JobOutput) (CostEstimate, error)
}

// Definition binds the three technical components for one job_type.
type Definition struct {
	JobType       string
	Validator     Validator
	Compiler      Compiler
	CostEstimator CostEstimator
}

// CompiledSpec is the compiler boundary. Spec is the normalized JSON sent
// downstream; OperationCount is metadata for observability and future DAG
// compilation, not a second wire contract.
type CompiledSpec struct {
	JobType        string
	Spec           json.RawMessage
	OperationCount int
}

// CostEstimate is advisory metadata emitted when a job is accepted.
type CostEstimate struct {
	RenderUnits         int64 `json:"render_units"`
	EstimatedDurationMS int64 `json:"estimated_duration_ms"`
	SceneCount          int   `json:"scene_count"`
	AssetCount          int   `json:"asset_count"`
}

// Registry resolves technical job definitions by exact job_type.
type Registry struct {
	definitions map[string]Definition
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{definitions: make(map[string]Definition)}
}

// Register adds or replaces a definition. It rejects malformed definitions
// so Resolve never returns a nil validator/compiler/estimator.
func (r *Registry) Register(def Definition) error {
	if r == nil {
		return fmt.Errorf("%w: registry is nil", ErrInvalidDefinition)
	}
	jobType := strings.TrimSpace(def.JobType)
	if jobType == "" || def.Validator == nil || def.Compiler == nil || def.CostEstimator == nil {
		return fmt.Errorf("%w: job_type and all components are required", ErrInvalidDefinition)
	}
	if r.definitions == nil {
		r.definitions = make(map[string]Definition)
	}
	def.JobType = jobType
	r.definitions[jobType] = def
	return nil
}

// Resolve returns the complete technical definition for jobType.
func (r *Registry) Resolve(jobType string) (Definition, error) {
	if r == nil {
		return Definition{}, fmt.Errorf("%w: registry is nil", ErrUnknownJobType)
	}
	def, ok := r.definitions[strings.TrimSpace(jobType)]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %q", ErrUnknownJobType, jobType)
	}
	return def, nil
}

// Names returns registered job types in stable order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NewDefaultRegistry registers the technical job types currently supported
// by the canonical contract. Adding an editorial format should change a
// template, not this list; adding a renderer behavior belongs here.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	for _, jobType := range []string{
		"audio.mux.v1",
		"clip.stock.v1",
		"microcut.batch.v1",
		"scene.composite.v1",
		"scene.image.v1",
		"slideshow.v1",
	} {
		definition := Definition{
			JobType:       jobType,
			Validator:     objectSpecValidator{requiredArray: requiredArrayFor(jobType)},
			Compiler:      canonicalJSONCompiler{jobType: jobType},
			CostEstimator: profileCostEstimator{jobType: jobType},
		}
		if err := r.Register(definition); err != nil {
			panic("veloxjobs: build default registry: " + err.Error())
		}
	}
	return r
}

func requiredArrayFor(jobType string) string {
	switch jobType {
	case "audio.mux.v1":
		return "tracks"
	case "microcut.batch.v1":
		return "clips"
	case "scene.image.v1", "scene.composite.v1", "clip.stock.v1":
		return "scenes"
	case "slideshow.v1":
		return "images"
	default:
		return ""
	}
}

type objectSpecValidator struct {
	requiredArray string
}

func (v objectSpecValidator) Validate(spec json.RawMessage) error {
	trimmed := bytes.TrimSpace(spec)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return errors.New("spec must be a valid JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return fmt.Errorf("spec object: %w", err)
	}
	if v.requiredArray == "" {
		return nil
	}
	raw, ok := object[v.requiredArray]
	if !ok {
		return fmt.Errorf("spec.%s is required for this job_type", v.requiredArray)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("spec.%s must be an array", v.requiredArray)
	}
	return nil
}

type canonicalJSONCompiler struct {
	jobType string
}

func (c canonicalJSONCompiler) Compile(spec json.RawMessage) (CompiledSpec, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(spec, &object); err != nil {
		return CompiledSpec{}, fmt.Errorf("compile %s spec: %w", c.jobType, err)
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return CompiledSpec{}, fmt.Errorf("compile %s spec: %w", c.jobType, err)
	}
	return CompiledSpec{
		JobType:        c.jobType,
		Spec:           normalized,
		OperationCount: operationCount(object),
	}, nil
}

func operationCount(object map[string]json.RawMessage) int {
	count := 0
	for _, key := range []string{"scenes", "images", "clips", "tracks"} {
		var values []json.RawMessage
		if err := json.Unmarshal(object[key], &values); err == nil {
			count += len(values)
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

type profileCostEstimator struct {
	jobType string
}

func (e profileCostEstimator) Estimate(spec json.RawMessage, output *veloxcontract.JobOutput) (CostEstimate, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(spec, &object); err != nil {
		return CostEstimate{}, fmt.Errorf("estimate %s spec: %w", e.jobType, err)
	}
	countArray := func(key string) int {
		var values []json.RawMessage
		if err := json.Unmarshal(object[key], &values); err != nil {
			return 0
		}
		return len(values)
	}
	scenes := countArray("scenes")
	assets := scenes + countArray("images") + countArray("clips") + countArray("tracks")
	if assets == 0 {
		assets = 1
	}
	pixels := int64(1)
	if output != nil && output.Width > 0 && output.Height > 0 {
		pixels = int64(output.Width) * int64(output.Height) / (1920 * 1080)
		if pixels < 1 {
			pixels = 1
		}
	}
	base := int64(1)
	switch e.jobType {
	case "scene.composite.v1", "microcut.batch.v1":
		base = 2
	case "audio.mux.v1":
		base = 1
	}
	return CostEstimate{
		RenderUnits:         base * int64(assets) * pixels,
		EstimatedDurationMS: int64(maxInt(scenes, assets)) * 1000,
		SceneCount:          scenes,
		AssetCount:          assets,
	}, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
