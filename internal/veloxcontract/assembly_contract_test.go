package veloxcontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPreparationManifestValidate(t *testing.T) {
	manifest := PreparationManifest{
		ContractVersion: CanonicalAssemblyContractV1,
		JobID:           "job-123",
		PreparationHash: strings.Repeat("a", 64),
		ExpectedProfile: AssemblyProfileVeloxH264CopyV1,
		Revision:        1,
		Assets: []AssemblyAsset{{
			AssetID: "clip-1", Kind: AssemblyAssetSourceClip,
			SHA256: strings.Repeat("b", 64), SizeBytes: 42, Required: true,
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestPreparationManifestRejectsInvalidSHAAndDuplicateAsset(t *testing.T) {
	manifest := PreparationManifest{
		ContractVersion: CanonicalAssemblyContractV1,
		JobID:           "job-123", PreparationHash: strings.Repeat("a", 64), Revision: 1,
		Assets: []AssemblyAsset{
			{AssetID: "clip-1", Kind: AssemblyAssetSourceClip, SHA256: strings.Repeat("b", 64)},
			{AssetID: "clip-1", Kind: AssemblyAssetSourceClip, SHA256: "BAD"},
		},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected duplicate asset rejection")
	}
	manifest.Assets[1].AssetID = "clip-2"
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected invalid SHA rejection")
	}
}

func TestPreparationHashIsOrderIndependent(t *testing.T) {
	assets := []AssemblyAsset{
		{AssetID: "b", Kind: AssemblyAssetSourceClip, SHA256: strings.Repeat("b", 64)},
		{AssetID: "a", Kind: AssemblyAssetSourceClip, SHA256: strings.Repeat("a", 64)},
	}
	mapping := json.RawMessage(`{"scenes":[{"id":"scene-1","asset_id":"a"}]}`)
	first, err := PreparationHash(AssemblyProfileVeloxH264CopyV1, assets, mapping)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PreparationHash(AssemblyProfileVeloxH264CopyV1, []AssemblyAsset{assets[1], assets[0]}, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("hash is not deterministic: %q vs %q", first, second)
	}
}

func TestFinalManifestDeltaValidateTimeline(t *testing.T) {
	delta := FinalManifestDelta{
		ContractVersion: CanonicalAssemblyContractV1,
		JobID:           "job-123", Revision: 2,
		Timeline: &AssemblyTimeline{
			Revision: TimelineRevision{Revision: 2, SHA256: strings.Repeat("c", 64)},
			Data:     json.RawMessage(`{"tracks":[]}`),
		},
	}
	if err := delta.Validate(); err != nil {
		t.Fatalf("valid delta rejected: %v", err)
	}
	delta.Timeline.Revision.Revision = 0
	if err := delta.Validate(); err == nil {
		t.Fatal("expected invalid timeline revision")
	}
}
