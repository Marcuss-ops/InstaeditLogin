package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestConvertCatalogEntriesIncludesOpaqueDestination(t *testing.T) {
	entries := convertCatalogEntries([]deliveries.CatalogTargetEntry{{
		ResolvedTargetEntry: deliveries.ResolvedTargetEntry{
			PlatformAccountID: 381,
			Platform:          models.PlatformYouTube,
			ChannelID:         "UCready",
			ChannelName:       "Ready",
			Status:            models.AccountStatusActive,
			Enabled:           true,
		},
		ExternalDestinationID: "extdst_ready",
	}})
	if len(entries) != 1 || entries[0].ExternalDestinationID != "extdst_ready" || entries[0].CanPost == nil || !*entries[0].CanPost {
		t.Fatalf("catalog entry = %#v", entries)
	}
	if entries[0].Capabilities == nil || !entries[0].Capabilities.UploadVideo {
		t.Fatalf("catalog capabilities = %#v", entries[0].Capabilities)
	}
}

func TestValidationResponseOmitsCatalogOnlyFields(t *testing.T) {
	entries := convertResolvedEntries([]deliveries.ResolvedTargetEntry{{
		PlatformAccountID: 381,
		Platform:          models.PlatformYouTube,
		ChannelID:         "UCready",
		Status:            models.AccountStatusActive,
		Enabled:           true,
	}})
	body, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(body)
	for _, forbidden := range []string{"can_post", "capabilities", "external_destination_id"} {
		if strings.Contains(wire, forbidden) {
			t.Fatalf("validation response leaked catalog field %q: %s", forbidden, wire)
		}
	}
}
