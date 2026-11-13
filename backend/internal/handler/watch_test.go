package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The watch handlers validate before any CS3 or Synaplan call, so their
// validation runs against a bare Handler.

func TestWatchStartRejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"not JSON", `{not json`, "invalid JSON body"},
		{"no target", `{"targetKind":"folder","preset":"keep_searchable","groupKey":"A"}`, "Choose a folder or a file."},
		{"unknown kind", `{"targetKind":"space","targetId":"r1","preset":"keep_searchable","groupKey":"A"}`, "Choose a folder or a file."},
		{"unknown preset", `{"targetKind":"folder","targetId":"r1","preset":"translate"}`, "Choose what should happen."},
		{"searchable without a folder", `{"targetKind":"folder","targetId":"r1","preset":"keep_searchable","groupKey":"  "}`, "Choose a knowledge folder."},
		{
			"searchable with a long folder name",
			`{"targetKind":"folder","targetId":"r1","preset":"keep_searchable","groupKey":"` + strings.Repeat("x", maxGroupKey+1) + `"}`,
			"too long",
		},
		{"task without a task", `{"targetKind":"file","targetId":"r1","preset":"task"}`, "Choose one of your tasks."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/watches", strings.NewReader(tc.body))
			(&Handler{}).WatchStart(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Fatalf("body = %q, want contains %q", rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestWatchStartAcceptsValidInput(t *testing.T) {
	cases := []string{
		`{"targetKind":"folder","targetId":"r1","preset":"keep_searchable","groupKey":"INVOICES"}`,
		`{"targetKind":"file","targetId":"r1","preset":"task","taskId":3}`,
	}
	for _, body := range cases {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/synaplan/watches", strings.NewReader(body))
		(&Handler{}).WatchStart(rr, req)
		if rr.Code == http.StatusBadRequest {
			t.Fatalf("validation rejected %s: %s", body, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "file_not_found") {
			t.Fatalf("past validation a bare handler cannot read the item, got %s", rr.Body.String())
		}
	}
}

func TestWatchRoutesNeedATarget(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		name   string
		method string
		path   string
		serve  http.HandlerFunc
	}{
		{"options", http.MethodGet, "/api/synaplan/watches/options", h.WatchOptions},
		{"stop", http.MethodDelete, "/api/synaplan/watches?targetId=%20", h.WatchStop},
		{"check now", http.MethodPost, "/api/synaplan/watches/reconcile", h.WatchReconcileNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.serve(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "targetId is required") {
				t.Fatalf("got %d %s, want 400 targetId is required", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestWatchFailure(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantText   string
	}{
		{"the cap names its number", upstream(409, []byte(`{"error":"watch_cap","message":"You can watch 20 items."}`)), 409, "watch_cap", "You can watch 20 items."},
		{"no linked cloud", upstream(403, []byte(`{"error":"platform_disconnected","message":"This key is not linked to a cloud."}`)), 409, "cloud_not_linked", "Synaplan does not know this OpenCloud yet."},
		{"two linked clouds", upstream(409, []byte(`{"error":"platform_ambiguous","message":"More than one cloud."}`)), 409, "cloud_not_linked", "Synaplan does not know this OpenCloud yet."},
		{"watches turned off", upstream(404, []byte(`{"error":"feature_not_configured","module":"cloud_files"}`)), 404, "watches_off", "turned off"},
		{"a refused request", upstream(400, []byte(`{"error":"invalid_watch","message":"Choose one of your tasks."}`)), 400, "watch_invalid", "Choose one of your tasks."},
		{"an upstream failure", upstream(500, []byte(`oops`)), 502, "synaplan_error", "Synaplan could not finish this."},
		{"no answer", errors.New("connection refused"), 502, "synaplan_error", "Synaplan could not finish this."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := watchFailure(tc.err)
			if status != tc.wantStatus || body.Code != tc.wantCode || !strings.Contains(body.Error, tc.wantText) {
				t.Fatalf("got %d %+v, want %d %s %q", status, body, tc.wantStatus, tc.wantCode, tc.wantText)
			}
		})
	}
}

func TestWatchStopStopsEveryWatchOnTheTarget(t *testing.T) {
	syn := newFakeSynaplan(t, []watch{
		{ID: 1, TargetKind: "folder", TargetID: "s$sp!f", State: "active"},
		{ID: 2, TargetKind: "folder", TargetID: "s$sp!other", State: "active"},
		{ID: 3, TargetKind: "folder", TargetID: "s$sp!f", State: "paused"},
	})
	syn.stopStatus[3] = http.StatusNotFound
	h := syn.handler(t)

	rr := httptest.NewRecorder()
	h.WatchStop(rr, httptest.NewRequest(http.MethodDelete, "/api/synaplan/watches?targetId=s$sp!f", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var answer struct {
		Stopped int `json:"stopped"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &answer)
	if answer.Stopped != 1 {
		t.Fatalf("stopped %d, want 1 (the other one was gone already)", answer.Stopped)
	}
	if got := syn.stopped(); len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("stopped watches %v, want [1 3]", got)
	}
}

func TestWatchCheckNowTellsWhenTheCloudIsNotLinked(t *testing.T) {
	syn := newFakeSynaplan(t, nil)
	syn.listStatus = http.StatusForbidden
	syn.listBody = `{"error":"platform_disconnected","message":"This key is not linked to a cloud."}`
	h := syn.handler(t)

	rr := httptest.NewRecorder()
	h.WatchReconcileNow(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/watches/reconcile?targetId=s$sp!f", nil))

	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "cloud_not_linked") {
		t.Fatalf("got %d %s, want 409 cloud_not_linked", rr.Code, rr.Body.String())
	}
}
