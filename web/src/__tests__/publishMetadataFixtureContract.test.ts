import { describe, expect, test } from "vitest";
import fixture from "../../../api/fixtures/publish_metadata_fixture.json";

// ============================================================================
// CONTRACT FIXTURE TEST: TypeScript InstaEditor ↔ shared fixture
// ============================================================================
//
// This test validates that the TypeScript types used by InstaEditor
// are compatible with the shared publish metadata fixture at
// api/fixtures/publish_metadata_fixture.json.
//
// The fixture is the SINGLE SOURCE OF TRUTH for the publish metadata
// contract. Every field name, type, and constraint must be consistent
// across:
//   - Go DTO (publishYouTubeEditorSessionRequest)
//   - OpenAPI (YouTubeEditorSessionPublishRequest)
//   - NVIDIA schema (NVIDIAMetadataResponse)
//   - Validator backend (YouTubePublishOptions.Validate())
//   - TypeScript InstaEditor (this test)
//   - E2E tests

// ─── TypeScript types (must match the Go DTO and OpenAPI) ──────────

type YouTubeTranslation = {
  title: string;
  description: string;
};

type PublishMetadataPayload = {
  title: string;
  description: string;
  privacy_status: string;
  publish_at: string;
  tags: string[];
  default_language: string;
  default_audio_language: string;
  translations: Record<string, YouTubeTranslation>;
};

// ─── Tests ─────────────────────────────────────────────────────────

describe("Publish metadata fixture contract", () => {
  test("fixture is valid JSON and matches TypeScript types", () => {
    // TypeScript's structural typing validates the shape at compile time.
    // This runtime assertion confirms all expected keys exist.
    const payload = fixture as PublishMetadataPayload;

    expect(typeof payload.title).toBe("string");
    expect(payload.title.length).toBeGreaterThan(0);
    expect(payload.title.length).toBeLessThanOrEqual(100);

    expect(typeof payload.description).toBe("string");
    expect(payload.description.length).toBeLessThanOrEqual(5000);

    expect(["public", "unlisted", "private"]).toContain(payload.privacy_status);

    // publish_at must be a valid ISO-8601 string.
    expect(() => new Date(payload.publish_at)).not.toThrow();
    expect(new Date(payload.publish_at).getTime()).toBeGreaterThan(Date.now());

    expect(Array.isArray(payload.tags)).toBe(true);
    expect(payload.tags.length).toBeLessThanOrEqual(30);
    // Total tag chars + commas ≤ 500.
    const totalTagChars = payload.tags.join(",").length;
    expect(totalTagChars).toBeLessThanOrEqual(500);

    // BCP-47 codes.
    expect(payload.default_language).toMatch(/^[a-z]{2}(-[A-Z]{2})?$/);
    expect(payload.default_audio_language).toMatch(/^[a-z]{2}(-[A-Z]{2})?$/);

    // Translations must have at least one entry and require default_language.
    expect(typeof payload.translations).toBe("object");
    expect(Object.keys(payload.translations).length).toBeGreaterThan(0);

    for (const [lang, tr] of Object.entries(payload.translations)) {
      // Translation language key must be BCP-47-like.
      expect(lang).toMatch(/^[a-z]{2}(-[A-Z]{2})?$/);
      // Translation must not be identical to original.
      if (lang !== payload.default_language) {
        expect(tr.title).not.toBe(payload.title);
      }
      expect(typeof tr.title).toBe("string");
      expect(tr.title.length).toBeLessThanOrEqual(100);
      expect(typeof tr.description).toBe("string");
      expect(tr.description.length).toBeLessThanOrEqual(5000);
    }
  });

  test("fixture has no unknown keys", () => {
    const knownKeys = [
      "_comment",
      "title",
      "description",
      "privacy_status",
      "publish_at",
      "tags",
      "default_language",
      "default_audio_language",
      "translations",
    ];
    const fixtureKeys = Object.keys(fixture);
    const unknown = fixtureKeys.filter((k) => !knownKeys.includes(k));
    expect(unknown).toEqual([]);
  });

  test("fixture tags have no duplicates", () => {
    const payload = fixture as PublishMetadataPayload;
    const lower = payload.tags.map((t) => t.toLowerCase());
    const unique = new Set(lower);
    expect(unique.size).toBe(payload.tags.length);
  });

  test("fixture translations keys are valid BCP-47 codes", () => {
    const payload = fixture as PublishMetadataPayload;
    // All language codes in translations must be in the expected set.
    const validLangs = ["en", "es", "pt-BR"];
    for (const lang of Object.keys(payload.translations)) {
      expect(validLangs).toContain(lang);
    }
  });
});
