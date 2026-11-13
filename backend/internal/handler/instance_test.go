package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestInstanceStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		down         bool
		reachable    bool
		capabilities []string
	}{
		{name: "answers", status: http.StatusOK, body: `{"issuer":"https://synaplan.example","jwks_uri":"https://synaplan.example/.well-known/jwks.json","capabilities":["deliveries","pickups"]}`, reachable: true, capabilities: []string{"deliveries", "pickups"}},
		{name: "offers nothing", status: http.StatusOK, body: `{"issuer":"https://synaplan.example","jwks_uri":"https://synaplan.example/.well-known/jwks.json"}`, reachable: true, capabilities: []string{}},
		{name: "platform links off", status: http.StatusNotFound, body: `{"error":"not found"}`, capabilities: []string{}},
		{name: "failing", status: http.StatusInternalServerError, body: `oops`, capabilities: []string{}},
		{name: "down", down: true, capabilities: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/.well-known/synaplan-configuration" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" {
					t.Error("a credential was sent for an open document")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			url := synaplan.URL
			if tt.down {
				synaplan.Close()
			} else {
				defer synaplan.Close()
			}
			h, err := New(func(_ context.Context, req *http.Request) error {
				req.Header.Set("X-API-Key", "sk_shared")
				return nil
			}, url, nil, nil, Admin{})
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			h.InstanceStatus(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/instance", nil))

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var got instanceStatus
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Reachable != tt.reachable || !reflect.DeepEqual(got.Capabilities, tt.capabilities) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
