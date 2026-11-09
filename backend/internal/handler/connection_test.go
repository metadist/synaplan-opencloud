package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBindRefusesWhenTheInstanceIsNotRegistered(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/connection", nil)
	(&Handler{}).Bind(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "not_connected") {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}
