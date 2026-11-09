package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WatchStart rejects a bad request before it stats the file, so the
// cases below run against a bare Handler.

func TestWatchStartRejectsABadRequest(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"not json", "{not json", "invalid JSON body"},
		{"neither folder nor file", `{"targetKind":"space","targetId":"1","preset":"keep_searchable"}`, "Choose a folder or a file."},
		{"unknown preset", `{"targetKind":"folder","targetId":"1","preset":"nope"}`, "Choose what should happen."},
		{"task without an id", `{"targetKind":"file","targetId":"1","preset":"task"}`, "Choose one of your tasks."},
		{"missing target", `{"targetKind":"folder","preset":"keep_searchable"}`, "Choose a folder or a file."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/watches", strings.NewReader(tc.body))
			(&Handler{}).WatchStart(rr, req)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestWatchStartReachesStorageForAValidWatch(t *testing.T) {
	// A bare Handler has no CS3 gateway. Getting past validation
	// shows up as the storage failure, not another 400.
	rr := httptest.NewRecorder()
	body := `{"targetKind":"folder","targetId":"1","preset":"keep_searchable"}`
	req := httptest.NewRequest(http.MethodPost, "/api/synaplan/watches", strings.NewReader(body))
	(&Handler{}).WatchStart(rr, req)
	if rr.Code == http.StatusBadRequest {
		t.Fatalf("validation rejected a complete watch: %s", rr.Body.String())
	}
}

func TestWatchStopRequiresATarget(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/synaplan/watches", nil)
	(&Handler{}).WatchStop(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "targetId is required") {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}
