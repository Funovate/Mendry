package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"mendry/backend/internal/modules/observations/application"
)

func TestListPagination(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
		wantError  bool
	}{
		{name: "defaults", wantLimit: application.DefaultListLimit},
		{name: "limit and offset", query: "limit=25&offset=50", wantLimit: 25, wantOffset: 50},
		{name: "maximum limit", query: "limit=100", wantLimit: 100},
		{name: "limit too large", query: "limit=101", wantError: true},
		{name: "negative offset", query: "offset=-1", wantError: true},
		{name: "invalid offset", query: "offset=next", wantError: true},
		{name: "duplicate offset", query: "offset=1&offset=2", wantError: true},
		{name: "unknown parameter", query: "cursor=1", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(nethttp.MethodGet, "/observations?"+test.query, nil)
			limit, offset, err := listPagination(request)
			if test.wantError {
				if err == nil {
					t.Fatalf("listPagination() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("listPagination() error = %v", err)
			}
			if limit != test.wantLimit || offset != test.wantOffset {
				t.Fatalf("listPagination() = (%d, %d), want (%d, %d)", limit, offset, test.wantLimit, test.wantOffset)
			}
		})
	}
}
