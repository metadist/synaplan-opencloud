package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// deliveringSynaplan answers the claim, download and result routes of
// delivery 7 and records what was reported.
type deliveringSynaplan struct {
	*httptest.Server
	mu        sync.Mutex
	downloads int
	reported  []deliveryResult
	bearers   []string
}

func newDeliveringSynaplan(t *testing.T, claimStatus int, claim map[string]any, file string) *deliveringSynaplan {
	t.Helper()
	s := &deliveringSynaplan{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.bearers = append(s.bearers, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform-links/deliveries/7/claim":
			w.WriteHeader(claimStatus)
			_ = json.NewEncoder(w).Encode(claim)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/platform-links/deliveries/7/file" && r.URL.Query().Get("ticket") == "t1":
			s.downloads++
			_, _ = io.WriteString(w, file)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/platform-links/deliveries/7/result":
			var got deliveryResult
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			s.reported = append(s.reported, got)
			_, _ = io.WriteString(w, `{"state":"`+got.Status+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// savedFiles stands in for the person's OpenCloud.
type savedFiles struct {
	fail  bool
	saved map[string]string
}

func (f *savedFiles) Save(_ context.Context, filename string, content []byte) (string, error) {
	if f.fail {
		return "", errors.New("read-only")
	}
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[filename] = string(content)
	return "Synaplan/Documents/" + filename, nil
}

func claimFor(sub string, edit func(map[string]any)) map[string]any {
	claim := map[string]any{
		"id":            "7",
		"external_id":   sub,
		"name":          "notes.txt",
		"size":          5,
		"target":        map[string]string{"kind": "kind_folder", "ref": ""},
		"download_path": "/api/v1/platform-links/deliveries/7/file?ticket=t1",
	}
	if edit != nil {
		edit(claim)
	}
	return claim
}

func TestDeliver(t *testing.T) {
	cases := map[string]struct {
		claimStatus int
		claim       map[string]any
		failWrite   bool
		want        deliveryResult
		wantErr     error
		wantSaved   bool
	}{
		"written to the person's Synaplan folder": {
			claimStatus: http.StatusOK,
			claim:       claimFor("ada", nil),
			want:        deliveryResult{Status: "delivered", RemotePath: "/Synaplan/Documents/notes.txt"},
			wantSaved:   true,
		},
		"a claim for another person is not written": {
			claimStatus: http.StatusOK,
			claim:       claimFor("eve", nil),
			want:        deliveryResult{Status: "failed", ErrorCode: "unauthorized"},
		},
		"a download path of another copy is not followed": {
			claimStatus: http.StatusOK,
			claim: claimFor("ada", func(c map[string]any) {
				c["download_path"] = "https://elsewhere.example/api/v1/platform-links/deliveries/7/file?ticket=t1"
			}),
			want: deliveryResult{Status: "failed", ErrorCode: "unreachable"},
		},
		"a folder target is not supported yet": {
			claimStatus: http.StatusOK,
			claim: claimFor("ada", func(c map[string]any) {
				c["target"] = map[string]string{"kind": "folder_id", "ref": "space$root!folder"}
			}),
			want: deliveryResult{Status: "failed", ErrorCode: "unsupported"},
		},
		"too large": {
			claimStatus: http.StatusOK,
			claim:       claimFor("ada", func(c map[string]any) { c["size"] = maxDeliveryBytes + 1 }),
			want:        deliveryResult{Status: "failed", ErrorCode: "too_large"},
		},
		"the write fails": {
			claimStatus: http.StatusOK,
			claim:       claimFor("ada", nil),
			failWrite:   true,
			want:        deliveryResult{Status: "failed", ErrorCode: "unreachable"},
		},
		"already claimed": {
			claimStatus: http.StatusConflict,
			claim:       map[string]any{"error": "already_claimed"},
			wantErr:     errClaimRefused,
		},
		"Synaplan fails": {
			claimStatus: http.StatusInternalServerError,
			claim:       map[string]any{},
			wantErr:     errClaimLater,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synaplan := newDeliveringSynaplan(t, tc.claimStatus, tc.claim, "hello")
			h, err := New(nil, synaplan.URL, nil, delegated(t, fakeIdentityProvider(t, "ada"), "ada"))
			if err != nil {
				t.Fatal(err)
			}
			files := &savedFiles{fail: tc.failWrite}

			got, err := h.deliver(onBehalfOf(t.Context(), "ada"), files, "ada", 7)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				if len(synaplan.reported) != 0 {
					t.Fatalf("reported %v for a copy that was not claimed", synaplan.reported)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("result %+v, want %+v", got, tc.want)
			}
			if len(synaplan.reported) != 1 || synaplan.reported[0] != tc.want {
				t.Fatalf("reported %+v, want %+v", synaplan.reported, tc.want)
			}
			if tc.wantSaved != (files.saved["notes.txt"] == "hello") {
				t.Fatalf("saved %v, want saved %v", files.saved, tc.wantSaved)
			}
			if !tc.wantSaved && synaplan.downloads != 0 && !tc.failWrite {
				t.Fatalf("downloaded a copy that was not written")
			}
			for _, bearer := range synaplan.bearers {
				if bearer != "Bearer for-synaplan-app" {
					t.Fatalf("Synaplan saw %q, want the person's own token", bearer)
				}
			}
		})
	}
}

// Nobody can claim a copy without the person's own token. The notification
// is answered so that Synaplan's retry is let in once they allowed it.
func TestADeliveryForAPersonWhoDidNotAllowItIsLeftForTheRetry(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	h, err := New(nil, synaplan.URL, nil, delegated(t, fakeIdentityProvider(t, "ada"), "ada"))
	if err != nil {
		t.Fatal(err)
	}
	token := synaplan.notification(t, map[string]any{"type": "delivery", "id": "7", "sub": "eve"})

	for range 2 {
		rec := inbound(h, token)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_connected") {
			t.Fatalf("status %d %s, want 409 not_connected", rec.Code, rec.Body)
		}
	}
}

func TestADeliveryNotificationNeedsANumericID(t *testing.T) {
	synaplan := newSignedSynaplan(t)
	defer synaplan.Close()
	h, err := New(nil, synaplan.URL, nil, delegated(t, fakeIdentityProvider(t, "ada"), "ada"))
	if err != nil {
		t.Fatal(err)
	}
	token := synaplan.notification(t, map[string]any{"type": "delivery", "id": "seven", "sub": "ada"})

	if rec := inbound(h, token); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestDownloadTicket(t *testing.T) {
	cases := map[string]struct {
		path string
		want string
		ok   bool
	}{
		"this copy":          {"/api/v1/platform-links/deliveries/7/file?ticket=t1", "t1", true},
		"another copy":       {"/api/v1/platform-links/deliveries/8/file?ticket=t1", "", false},
		"another host":       {"https://elsewhere.example/api/v1/platform-links/deliveries/7/file?ticket=t1", "", false},
		"no ticket":          {"/api/v1/platform-links/deliveries/7/file", "", false},
		"another route":      {"/api/v1/files/7?ticket=t1", "", false},
		"a path that climbs": {"/api/v1/platform-links/deliveries/7/../8/file?ticket=t1", "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := downloadTicket(7, tc.path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %q %v, want %q %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
