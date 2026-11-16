package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestCloudStatusAnswers(t *testing.T) {
	row := `{"id":"s!f1","state":"searchable","file_id":9,"knowledge_folder":{"name":"Invoices"}}`
	cases := []struct {
		name        string
		status      int
		body        string
		enabled     bool
		unreachable bool
		items       string
	}{
		{"rows", http.StatusOK, `{"items":[` + row + `],"watches":[]}`, true, false, `[` + row + `]`},
		{"turned off", http.StatusNotFound, `{"error":"feature_not_configured"}`, false, false, `[]`},
		{"failing", http.StatusInternalServerError, `oops`, true, true, `[]`},
		{"unreadable", http.StatusOK, `<html>`, true, true, `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/cloud/status" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil, Admin{})
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/cloud/status", strings.NewReader(`{"items":[{"id":"s!f1","version":"v1"}]}`))
			h.CloudStatus(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var got struct {
				Enabled     bool            `json:"enabled"`
				Unreachable bool            `json:"unreachable"`
				Items       json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Enabled != tc.enabled || got.Unreachable != tc.unreachable || string(got.Items) != tc.items {
				t.Fatalf("got %s", rr.Body.String())
			}
		})
	}
}
