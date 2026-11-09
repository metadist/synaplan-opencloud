package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Cloud status validation runs before any Synaplan call, so a bare
// Handler is enough. The same is true of the file id on the sidebar.

func TestCloudStatusRejectsTheWrongNumberOfFiles(t *testing.T) {
	tooMany := `{"items":[` + strings.Repeat(`{"id":"1","version":"v"},`, 500) + `{"id":"1","version":"v"}]}`
	cases := []struct {
		name, body string
	}{
		{"not json", "{not json"},
		{"none", `{"items":[]}`},
		{"more than 500", tooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/cloud/status", strings.NewReader(tc.body))
			(&Handler{}).CloudStatus(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCloudDetailRequiresAFileID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/cloud/files", nil)
	(&Handler{}).CloudDetail(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "fileId is required") {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}
