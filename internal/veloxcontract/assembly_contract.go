package veloxcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CanonicalAssemblyContractV1 is the immutable wire contract shared by
// PipelineGen, Chronon/RenderingGen and Velox for preparation and assembly.
const CanonicalAssemblyContractV1 = "velox.assembly.v1"

// AssemblyAssetKind identifies the role of an asset in an assembly.
type AssemblyAssetKind string

const (
	AssemblyAssetSourceClip   AssemblyAssetKind = "source_clip"
	AssemblyAssetVoiceover    AssemblyAssetKind = "voiceover"
	AssemblyAssetMusic        AssemblyAssetKind = "music"
	AssemblyAssetOverlay      AssemblyAssetKind = "overlay"
	AssemblyAssetGeneratedScene AssemblyAssetKind = "generated_scene"
	AssemblyAssetPreparedScene AssemblyAssetKind = "prepared_scene"
	AssemblyAssetSubtitle     AssemblyAssetKind = "subtitle"
)

// AssemblyAssetState describes asset readiness on a specific worker.
type AssemblyAssetState string

const (
	AssemblyAssetWaiting    AssemblyAssetState = "WAITING"
	AssemblyAssetPrefetching AssemblyAssetState = "PREFETCHING"
	AssemblyAssetReady      AssemblyAssetState = "READY"
	AssemblyAssetMissing    AssemblyAssetState = "MISSING"
	AssemblyAssetInvalid    AssemblyAssetState = "INVALID"
)

// AssemblyJobState describes the preparation/execution lifecycle.
type AssemblyJobState string

const (
	AssemblyJobPending    AssemblyJobState = "PENDING"
	AssemblyJobPrefetching AssemblyJobState = "PREFETCHING"
	AssemblyJobPrepared   AssemblyJobState = "PREPARED"
	AssemblyJobWaitingFinal AssemblyJobState = "WAITING_FINAL_MANIFEST"
	AssemblyJobExecuting  AssemblyJobState = "EXECUTING"
	AssemblyJobCompleted  AssemblyJobState = "COMPLETED"
	AssemblyJobExpired    AssemblyJobState = "EXPIRED"
	AssemblyJobFailed     AssemblyJobState = "FAILED"
	AssemblyJobCancelled  AssemblyJobState = "CANCELLED"
)

// AssemblyProfileID identifies a deterministic media profile.
type AssemblyProfileID string

const AssemblyProfileVeloxH264CopyV1 AssemblyProfileID = "velox-h264-copy-v1"

// AssemblyProfile describes the compatibility requirements for an asset.
type AssemblyProfile struct {
	ID            AssemblyProfileID `json:"profile_id"`
	Container     string            `json:"container"`
	VideoCodec    string            `json:"video_codec"`
	AudioCodec    string            `json:"audio_codec"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	FPS           int               `json:"fps"`
	AllowCopy     bool              `json:"allow_copy"`
}

// AssemblyAsset is a content-addressed asset reference. SHA256 is mandatory
// and is the only identity used for local cache reuse.
type AssemblyAsset struct {
	AssetID       string             `json:"asset_id"`
	Kind          AssemblyAssetKind  `json:"kind"`
	SHA256        string             `json:"sha256"`
	URL           string             `json:"url,omitempty"`
	SizeBytes     int64              `json:"size_bytes"`
	MimeType      string             `json:"mime_type,omitempty"`
	ProfileID     AssemblyProfileID  `json:"profile_id,omitempty"`
	State         AssemblyAssetState `json:"state,omitempty"`
	Required      bool               `json:"required"`
	SceneID       string             `json:"scene_id,omitempty"`
}

// TimelineRevision makes timeline updates monotonic and idempotent.
type TimelineRevision struct {
	Revision  int    `json:"revision"`
	SHA256    string `json:"sha256"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// AssemblyTimeline is the canonical timeline plus its content hash.
type AssemblyTimeline struct {
	Revision TimelineRevision `json:"revision"`
	Data     json.RawMessage  `json:"data"`
}

// PreparationManifest is the small initial manifest used for eager prefetch.
type PreparationManifest struct {
	ContractVersion string             `json:"contract_version"`
	JobID           string             `json:"job_id"`
	PreparationHash string             `json:"preparation_hash"`
	ExpectedProfile AssemblyProfileID  `json:"expected_profile"`
	Revision        int                `json:"revision"`
	Assets          []AssemblyAsset    `json:"assets"`
}

// FinalManifestDelta carries only assets and fields produced after prefetch.
type FinalManifestDelta struct {
	ContractVersion string            `json:"contract_version"`
	JobID           string            `json:"job_id"`
	Revision        int               `json:"revision"`
	Timeline        *AssemblyTimeline `json:"timeline,omitempty"`
	Assets          []AssemblyAsset   `json:"assets,omitempty"`
}

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateAssemblySHA256(value string) error {
	if !sha256Pattern.MatchString(value) {
		return errors.New("sha256 must be exactly 64 lowercase hexadecimal characters")
	}
	return nil
}

func (a AssemblyAsset) Validate() error {
	if strings.TrimSpace(a.AssetID) == "" {
		return errors.New("asset_id is required")
	}
	if a.Kind == "" {
		return errors.New("kind is required")
	}
	if err := validateAssemblySHA256(a.SHA256); err != nil {
		return fmt.Errorf("asset %s: %w", a.AssetID, err)
	}
	if a.SizeBytes < 0 {
		return fmt.Errorf("asset %s: size_bytes must not be negative", a.AssetID)
	}
	return nil
}

func (r TimelineRevision) Validate() error {
	if r.Revision <= 0 {
		return errors.New("timeline revision must be positive")
	}
	return validateAssemblySHA256(r.SHA256)
}

func (m PreparationManifest) Validate() error {
	if m.ContractVersion != CanonicalAssemblyContractV1 {
		return fmt.Errorf("contract_version must be %q", CanonicalAssemblyContractV1)
	}
	if strings.TrimSpace(m.JobID) == "" {
		return errors.New("job_id is required")
	}
	if strings.TrimSpace(m.PreparationHash) == "" {
		return errors.New("preparation_hash is required")
	}
	if m.Revision <= 0 {
		return errors.New("revision must be positive")
	}
	if len(m.Assets) == 0 {
		return errors.New("assets must be non-empty")
	}
	seen := make(map[string]struct{}, len(m.Assets))
	for _, asset := range m.Assets {
		if err := asset.Validate(); err != nil {
			return err
		}
		if _, ok := seen[asset.AssetID]; ok {
			return fmt.Errorf("duplicate asset_id %q", asset.AssetID)
		}
		seen[asset.AssetID] = struct{}{}
	}
	return nil
}

func (d FinalManifestDelta) Validate() error {
	if d.ContractVersion != CanonicalAssemblyContractV1 {
		return fmt.Errorf("contract_version must be %q", CanonicalAssemblyContractV1)
	}
	if strings.TrimSpace(d.JobID) == "" || d.Revision <= 0 {
		return errors.New("job_id and positive revision are required")
	}
	for _, asset := range d.Assets {
		if err := asset.Validate(); err != nil {
			return err
		}
	}
	if d.Timeline != nil {
		if err := d.Timeline.Revision.Validate(); err != nil {
			return fmt.Errorf("timeline: %w", err)
		}
		if len(d.Timeline.Data) == 0 || !json.Valid(d.Timeline.Data) {
			return errors.New("timeline.data must be valid JSON")
		}
	}
	return nil
}

// PreparationHash computes a stable hash from sorted source identities,
// their SHA256 values, the expected profile and the timeline/source mapping.
func PreparationHash(profile AssemblyProfileID, assets []AssemblyAsset, timelineMapping json.RawMessage) (string, error) {
	type identity struct {
		AssetID string            `json:"asset_id"`
		Kind    AssemblyAssetKind `json:"kind"`
		SHA256  string            `json:"sha256"`
	}
	items := make([]identity, 0, len(assets))
	for _, asset := range assets {
		if err := asset.Validate(); err != nil {
			return "", err
		}
		items = append(items, identity{asset.AssetID, asset.Kind, asset.SHA256})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AssetID < items[j].AssetID })
	canonical, err := json.Marshal(struct {
		Profile AssemblyProfileID `json:"profile"`
		Assets  []identity        `json:"assets"`
		Mapping json.RawMessage   `json:"mapping"`
	}{profile, items, timelineMapping})
	if err != nil {
		return "", fmt.Errorf("marshal preparation identity: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
