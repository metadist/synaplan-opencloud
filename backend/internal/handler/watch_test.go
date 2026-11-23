package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v3/pkg/ctx"
	"github.com/opencloud-eu/reva/v3/pkg/rgrpc/todo/pool"
	"google.golang.org/grpc"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
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

type staticGateway struct {
	gateway.GatewayAPIClient
	auth *gateway.AuthenticateResponse
	stat *provider.StatResponse
	list *provider.ListContainerResponse
}

func (g *staticGateway) Authenticate(context.Context, *gateway.AuthenticateRequest, ...grpc.CallOption) (*gateway.AuthenticateResponse, error) {
	return g.auth, nil
}

func (g *staticGateway) Stat(context.Context, *provider.StatRequest, ...grpc.CallOption) (*provider.StatResponse, error) {
	return g.stat, nil
}

func (g *staticGateway) ListContainer(context.Context, *provider.ListContainerRequest, ...grpc.CallOption) (*provider.ListContainerResponse, error) {
	return g.list, nil
}

type staticSelector struct{ gw gateway.GatewayAPIClient }

func (s staticSelector) Next(...pool.Option) (gateway.GatewayAPIClient, error) { return s.gw, nil }

func TestWatchStartRejectsKindMismatch(t *testing.T) {
	gw := &staticGateway{
		stat: &provider.StatResponse{
			Status: &rpc.Status{Code: rpc.Code_CODE_OK},
			Info: &provider.ResourceInfo{
				Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "f"},
				Name: "note.txt", Path: "./note.txt", Etag: "e1",
				Type: provider.ResourceType_RESOURCE_TYPE_FILE,
			},
		},
	}
	h := &Handler{cs3: cs3reader.New(staticSelector{gw}, false)}
	ctx := revactx.ContextSetToken(context.Background(), "reva-ada")
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/synaplan/watches",
		strings.NewReader(`{"targetKind":"folder","targetId":"s$sp!f","preset":"keep_searchable","groupKey":"INVOICES"}`))

	h.WatchStart(rr, req)

	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"watch_invalid"`) {
		t.Fatalf("got %d %s, want 400 watch_invalid", rr.Code, rr.Body.String())
	}
}

func TestWatchRoutesRefuseASharedKey(t *testing.T) {
	syn := newFakeSynaplan(t, []watch{{ID: 1, TargetKind: "file", TargetID: "s$sp!f", State: "active"}})
	h := syn.handler(t)
	h.UseSharedKey()
	body := strings.NewReader(`{"targetKind":"file","targetId":"s$sp!f","preset":"keep_searchable","groupKey":"INVOICES"}`)
	cases := []struct {
		name   string
		method string
		path   string
		body   io.Reader
		serve  http.HandlerFunc
	}{
		{"options", http.MethodGet, "/api/synaplan/watches/options?targetId=s$sp!f", nil, h.WatchOptions},
		{"start", http.MethodPost, "/api/synaplan/watches", body, h.WatchStart},
		{"stop", http.MethodDelete, "/api/synaplan/watches?targetId=s$sp!f", nil, h.WatchStop},
		{"check now", http.MethodPost, "/api/synaplan/watches/reconcile?targetId=s$sp!f", nil, h.WatchReconcileNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.serve(rr, httptest.NewRequest(tc.method, tc.path, tc.body))
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), `"watch_shared_key"`) {
				t.Fatalf("got %d %s, want 403 watch_shared_key", rr.Code, rr.Body.String())
			}
		})
	}
	if got := syn.stopped(); len(got) != 0 {
		t.Fatalf("shared key stopped watches %v", got)
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

func TestWatchOptionsReportsWatchState(t *testing.T) {
	cases := []struct {
		name         string
		watches      []watch
		wantWatching bool
		wantState    string
	}{
		{"unwatched", nil, false, ""},
		{"active", []watch{{ID: 1, TargetID: "s$sp!f", State: "active"}}, true, "active"},
		{"paused", []watch{{ID: 1, TargetID: "s$sp!f", State: "paused"}}, true, "paused"},
		{"attention", []watch{{ID: 1, TargetID: "s$sp!f", State: "attention"}}, true, "attention"},
		{
			"active wins when paired with paused",
			[]watch{{ID: 1, TargetID: "s$sp!f", State: "paused"}, {ID: 2, TargetID: "s$sp!f", State: "active"}},
			true, "active",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			syn := newFakeSynaplan(t, tc.watches)
			rr := httptest.NewRecorder()
			syn.handler(t).WatchOptions(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/watches/options?targetId=s$sp!f", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			var got struct {
				Watching       bool   `json:"watching"`
				WatchState     string `json:"watchState"`
				UploadsBlocked string `json:"uploadsBlocked"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &got)
			if got.Watching != tc.wantWatching || got.WatchState != tc.wantState || got.UploadsBlocked != "machine_auth" {
				t.Fatalf("got %+v, want watching=%v state=%q blocked=machine_auth", got, tc.wantWatching, tc.wantState)
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
