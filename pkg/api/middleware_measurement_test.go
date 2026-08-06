package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMetricsMiddleware_RecordsRequestAndPayload(t *testing.T) {
	r := &Router{}
	pattern := "/measurement-test"

	handler := r.metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if metrics.RequestStatsFromContext(req.Context()) == nil {
			t.Fatal("request stats missing from context")
		}
		metrics.ObserveSQL(req.Context(), time.Millisecond)
		_, _ = w.Write([]byte("payload"))
	}))

	routeContext := chi.NewRouteContext()
	routeContext.RoutePatterns = []string{pattern}
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, routeContext)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, pattern, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if !metricFamilyHasRoute(families, "http_requests_total", pattern) {
		t.Fatalf("http_requests_total missing route %q", pattern)
	}
	if !metricFamilyHasRoute(families, "http_response_size_bytes", pattern) {
		t.Fatalf("http_response_size_bytes missing route %q", pattern)
	}
	if !metricFamilyHasRoute(families, "http_request_sql_queries", pattern) {
		t.Fatalf("http_request_sql_queries missing route %q", pattern)
	}
}

func metricFamilyHasRoute(families []*dto.MetricFamily, name, route string) bool {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "route" && label.GetValue() == route {
					return true
				}
			}
		}
	}
	return false
}
