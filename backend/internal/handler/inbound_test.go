package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInboundRejectsUnsigned(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/inbound", strings.NewReader(`{"type":"delivery","id":"9"}`))
	(&Handler{}).Inbound(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
