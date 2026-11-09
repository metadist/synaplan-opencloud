package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCopyFromSynaplanRejectsABadRequest(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"not json", "{not json", "invalid JSON body"},
		{"no file", `{"fileId":0,"name":"notes.txt"}`, "Choose a file."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/from-synaplan", strings.NewReader(tc.body))
			(&Handler{}).CopyFromSynaplan(rr, req)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCopyFromSynaplanReachesStorageForAChosenFile(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/from-synaplan", strings.NewReader(`{"fileId":4,"name":"notes.txt"}`))
	(&Handler{}).CopyFromSynaplan(rr, req)
	if rr.Code == http.StatusBadRequest {
		t.Fatalf("validation rejected a chosen file: %s", rr.Body.String())
	}
}
