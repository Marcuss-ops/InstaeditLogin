package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePerformanceSummaryFilters_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID, got %v", *filters.workspaceID)
	}
	if filters.group != "" {
		t.Fatalf("expected empty group, got %q", filters.group)
	}
	if filters.language != "" {
		t.Fatalf("expected empty language, got %q", filters.language)
	}
	if filters.manager != "" {
		t.Fatalf("expected empty manager, got %q", filters.manager)
	}
}

func TestParsePerformanceSummaryFilters_AllFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=7&group=Marketing&language=en&manager=Alice", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID == nil {
		t.Fatal("expected workspaceID, got nil")
	}
	if *filters.workspaceID != 7 {
		t.Fatalf("expected workspaceID 7, got %d", *filters.workspaceID)
	}
	if filters.group != "Marketing" {
		t.Fatalf("expected group Marketing, got %q", filters.group)
	}
	if filters.language != "en" {
		t.Fatalf("expected language en, got %q", filters.language)
	}
	if filters.manager != "Alice" {
		t.Fatalf("expected manager Alice, got %q", filters.manager)
	}
}

func TestParsePerformanceSummaryFilters_InvalidWorkspaceIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=not-a-number", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID for invalid value, got %v", *filters.workspaceID)
	}
}

func TestParsePerformanceSummaryFilters_NonPositiveWorkspaceIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=-5", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID for non-positive value, got %v", *filters.workspaceID)
	}
}
