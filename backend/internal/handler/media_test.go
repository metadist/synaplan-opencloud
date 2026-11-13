package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGeneratedFile(t *testing.T) {
	cases := []struct {
		in   string
		want generatedFile
		ok   bool
	}{
		{"/api/v1/files/uploads/04/000/00004/2026/11/media_4_test_1.png", generatedFile{uploadPath: "04/000/00004/2026/11/media_4_test_1.png"}, true},
		{"https://synaplan.example/api/v1/files/uploads/7/cat.png?x=1#y", generatedFile{uploadPath: "7/cat.png"}, true},
		{"/api/v1/files/uploads/generated_abc.png", generatedFile{uploadPath: "generated_abc.png"}, true},
		{"/api/v1/files/42/download", generatedFile{id: 42}, true},
		{"/api/v1/files/0/download", generatedFile{}, false},
		{"/api/v1/files/-1/download", generatedFile{}, false},
		{"/api/v1/files/42/content", generatedFile{}, false},
		{"/api/v1/files/uploads/", generatedFile{}, false},
		{"/api/v1/files/uploads/04/../../admin", generatedFile{}, false},
		{"/api/v1/files/uploads/04/./cat.png", generatedFile{}, false},
		{"/api/v1/files/uploads/a b/cat.png", generatedFile{}, false},
		{"/api/v1/files/uploads/a%2Fb/cat.png", generatedFile{}, false},
		{"/api/v1/files/uploads/1/2/3/4/5/6/7/8.png", generatedFile{}, false},
		{"/api/v1/admin/users", generatedFile{}, false},
		{"http://evil.example/", generatedFile{}, false},
		{"", generatedFile{}, false},
	}
	for _, tc := range cases {
		got, ok := parseGeneratedFile(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseGeneratedFile(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestGenerateImageRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, wantMsg string
	}{
		{"bad json", "{", "invalid JSON body"},
		{"no prompt", `{}`, "prompt is required"},
		{"blank prompt", `{"prompt":"  "}`, "prompt is required"},
		{"long prompt", `{"prompt":"` + strings.Repeat("a", maxImagePromptRunes+1) + `"}`, "prompt is too long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/media", strings.NewReader(tc.body))
			(&Handler{}).GenerateImage(rr, req)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Fatalf("status = %d, body %s; want 400 with %q", rr.Code, rr.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestGenerateImageAsksSynaplanForAnImage(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantURL    string
		wantCode   string
	}{
		{"made", http.StatusOK, `{"success":true,"file":{"url":"/api/v1/files/uploads/7/cat.png","type":"image","mimeType":"image/png"},"provider":"test","model":"m"}`, http.StatusOK, "/api/v1/files/uploads/7/cat.png", ""},
		{"no image model", http.StatusUnprocessableEntity, `{"error":"No model available for image generation"}`, http.StatusUnprocessableEntity, "", "image_unavailable"},
		{"provider failed", http.StatusInternalServerError, `{"error":"boom"}`, http.StatusBadGateway, "", "synaplan_error"},
		{"no file", http.StatusOK, `{"success":true,"file":{"url":""}}`, http.StatusBadGateway, "", "synaplan_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/media/generate" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				var got map[string]any
				_ = json.NewDecoder(r.Body).Decode(&got)
				if got["prompt"] != "a red square" || got["type"] != "image" {
					t.Errorf("request body = %v", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/media", strings.NewReader(`{"prompt":" a red square "}`))
			h.GenerateImage(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var got struct {
				URL  string `json:"url"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.URL != tc.wantURL || got.Code != tc.wantCode {
				t.Fatalf("got %s", rr.Body.String())
			}
		})
	}
}

func TestSaveImageRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, wantMsg string
		wantStatus          int
	}{
		{"bad json", "{", "invalid JSON body", http.StatusBadRequest},
		{"foreign url", `{"url":"/api/v1/admin/users","name":"a.png"}`, "does not name a file", http.StatusBadRequest},
		{"no name", `{"url":"/api/v1/files/uploads/7/cat.png"}`, "filename is required", http.StatusBadRequest},
		{"no storage", `{"url":"/api/v1/files/uploads/7/cat.png","name":"cat.png"}`, "storage is not configured", http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/media/save", strings.NewReader(tc.body))
			(&Handler{}).SaveImage(rr, req)
			if rr.Code != tc.wantStatus || !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Fatalf("status = %d, body %s; want %d with %q", rr.Code, rr.Body.String(), tc.wantStatus, tc.wantMsg)
			}
		})
	}
}

func TestFetchGeneratedReadsTheFileFromSynaplan(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nimage")
	cases := []struct {
		name     string
		ref      generatedFile
		wantPath string
		status   int
		body     []byte
		want     []byte
		wantErr  error
	}{
		{"sharded upload", generatedFile{uploadPath: "04/000/00004/2026/11/m.png"}, "/api/v1/files/uploads/04/000/00004/2026/11/m.png", http.StatusOK, png, png, nil},
		{"by id", generatedFile{id: 42}, "/api/v1/files/42/download", http.StatusOK, png, png, nil},
		{"too large", generatedFile{id: 42}, "/api/v1/files/42/download", http.StatusOK, bytes.Repeat([]byte("a"), 65), nil, errImageTooLarge},
		{"not theirs", generatedFile{id: 42}, "/api/v1/files/42/download", http.StatusForbidden, []byte(`{"error":"denied"}`), nil, errClientInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != tc.wantPath {
					t.Errorf("unexpected %s %s", r.Method, r.URL.EscapedPath())
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.SetMaxCopyBytes(64)

			got, err := h.fetchGenerated(context.Background(), tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || !bytes.Equal(got, tc.want) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestImagesStayClosedWithTheSharedKey(t *testing.T) {
	cases := []struct {
		name, path, body string
		serve            func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{"generate", "/api/synaplan/media", `{"prompt":"a red square"}`, (*Handler).GenerateImage},
		{"save", "/api/synaplan/media/save", `{"url":"/api/v1/files/42/download","name":"a.png"}`, (*Handler).SaveImage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
			h.UseSharedKey()
			rr := httptest.NewRecorder()
			tc.serve(h, rr, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), `"code":"shared_key"`) {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
		})
	}
}
