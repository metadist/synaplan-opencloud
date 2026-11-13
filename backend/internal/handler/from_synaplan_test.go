package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCopyLimitDefaultsToCloudflareAndCanRise(t *testing.T) {
	h := &Handler{}
	if h.copyLimit() != defaultMaxCopyBytes || defaultMaxCopyBytes != 100<<20 {
		t.Fatalf("default = %d", h.copyLimit())
	}
	h.SetMaxCopyBytes(4 << 30)
	if h.copyLimit() != 4<<30 {
		t.Fatalf("raised = %d", h.copyLimit())
	}
	h.SetMaxCopyBytes(0)
	if h.copyLimit() != 4<<30 {
		t.Fatal("zero must not clear a configured cap")
	}
}

func TestFromSynaplanRefusesSharedKey(t *testing.T) {
	h := &Handler{}
	h.UseSharedKey()
	rr := httptest.NewRecorder()
	h.ListSynaplanFiles(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/from-synaplan", nil))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), `"shared_key"`) {
		t.Fatalf("list status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	body := `{"fileId":1,"folderId":"s!f","name":"a.txt"}`
	h.CopyFromSynaplan(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/from-synaplan", strings.NewReader(body)))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), `"shared_key"`) {
		t.Fatalf("copy status = %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestCopyFromSynaplanRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"not json", "{", "invalid JSON body"},
		{"no file", `{"folderId":"s!f","name":"a.txt"}`, "fileId is required"},
		{"no folder", `{"fileId":3,"name":"a.txt"}`, "folderId is required"},
		{"no name", `{"fileId":3,"folderId":"s!f","name":" "}`, "name is required"},
		{"dot dot", `{"fileId":3,"folderId":"s!f","name":"../.."}`, "name is required"},
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

func TestListSynaplanFiles(t *testing.T) {
	cases := []struct {
		name, query, wantPage string
		status                int
		wantCode              int
	}{
		{"first page by default", "", "1", http.StatusOK, http.StatusOK},
		{"asked page", "?page=3", "3", http.StatusOK, http.StatusOK},
		{"nonsense page", "?page=-2", "1", http.StatusOK, http.StatusOK},
		{"synaplan fails", "", "1", http.StatusInternalServerError, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/api/v1/files" || q.Get("page") != tc.wantPage || q.Get("limit") != "50" {
					t.Errorf("unexpected %s?%s", r.URL.Path, r.URL.RawQuery)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"success":true,"files":[{"id":7,"filename":"a.txt"}]}`))
			}))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil, Admin{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			h.ListSynaplanFiles(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/from-synaplan"+tc.query, nil))
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			if tc.wantCode == http.StatusOK && !strings.Contains(rr.Body.String(), `"filename":"a.txt"`) {
				t.Fatalf("body %s", rr.Body.String())
			}
		})
	}
}
