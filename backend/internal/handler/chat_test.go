package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"not json", "{not json", "invalid JSON body"},
		{"no message", `{"resourceId":"r1"}`, "message is required"},
		{"blank message", `{"message":"   "}`, "message is required"},
		{"long message", `{"message":"` + strings.Repeat("a", chatMessageMaxLen+1) + `"}`, "message must be"},
		{"long model", `{"message":"hi","model":"` + strings.Repeat("m", chatModelMaxLen+1) + `"}`, "model must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/synaplan/chat", strings.NewReader(tc.body))
			(&Handler{}).Chat(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("body = %q, want contains %q", rr.Body.String(), tc.want)
			}
		})
	}
}

// fakeChatSynaplan answers the chat completion, the memory-service check
// and the memory search, and records the completion it was sent.
type fakeChatSynaplan struct {
	completionStatus int
	completionBody   string
	memoryAvailable  bool
	memories         string
	sent             map[string]any
	searches         int
}

func (f *fakeChatSynaplan) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &f.sent); err != nil {
				t.Errorf("completion body: %v", err)
			}
			w.WriteHeader(f.completionStatus)
			_, _ = w.Write([]byte(f.completionBody))
		case "/api/v1/config/memory-service/check":
			_ = json.NewEncoder(w).Encode(map[string]bool{"available": f.memoryAvailable})
		case "/api/v1/user/memories/search":
			f.searches++
			_, _ = w.Write([]byte(f.memories))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestChatAnswersAQuestion(t *testing.T) {
	answer := `{"choices":[{"message":{"role":"assistant","content":"Nordwind GmbH"}}]}`
	cases := []struct {
		name       string
		body       string
		fake       fakeChatSynaplan
		perPerson  bool
		wantStatus int
		wantCode   string
		wantModel  any
		wantLang   string
		wantMemory bool
	}{
		{
			name:       "default model, German",
			body:       `{"message":"Who sent it?","language":"de_DE"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusOK, completionBody: answer},
			wantStatus: http.StatusOK,
			wantModel:  nil,
			wantLang:   "German",
		},
		{
			name:       "picked model, unknown language",
			body:       `{"message":"Who sent it?","model":"gpt-4o","language":"xx"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusOK, completionBody: answer},
			wantStatus: http.StatusOK,
			wantModel:  "gpt-4o",
			wantLang:   "English",
		},
		{
			name: "memories as the person",
			body: `{"message":"Who sent it?"}`,
			fake: fakeChatSynaplan{
				completionStatus: http.StatusOK, completionBody: answer, memoryAvailable: true,
				memories: `{"memories":[{"key":"name","value":"Ada"},{"value":"likes short answers"},{"value":" "}]}`,
			},
			perPerson:  true,
			wantStatus: http.StatusOK,
			wantLang:   "English",
			wantMemory: true,
		},
		{
			name: "no memories with the shared key",
			body: `{"message":"Who sent it?"}`,
			fake: fakeChatSynaplan{
				completionStatus: http.StatusOK, completionBody: answer, memoryAvailable: true,
				memories: `{"memories":[{"key":"name","value":"Ada"}]}`,
			},
			wantStatus: http.StatusOK,
			wantLang:   "English",
		},
		{
			name:       "rate limited",
			body:       `{"message":"Who sent it?"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusTooManyRequests, completionBody: `{}`},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "rate_limited",
		},
		{
			name:       "failing",
			body:       `{"message":"Who sent it?"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusInternalServerError, completionBody: `Exception`},
			wantStatus: http.StatusBadGateway,
			wantCode:   "synaplan_error",
		},
		{
			name:       "empty answer",
			body:       `{"message":"Who sent it?"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusOK, completionBody: `{"choices":[{"message":{"content":" "}}]}`},
			wantStatus: http.StatusBadGateway,
			wantCode:   "synaplan_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			synaplan := httptest.NewServer(fake.handler(t))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.perPerson = tc.perPerson

			rr := httptest.NewRecorder()
			h.Chat(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/chat", strings.NewReader(tc.body)))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			var got struct {
				Answer string `json:"answer"`
				Code   string `json:"code"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if tc.wantCode != "" {
				if got.Code != tc.wantCode {
					t.Fatalf("code = %q, want %q", got.Code, tc.wantCode)
				}
				return
			}
			if got.Answer != "Nordwind GmbH" {
				t.Fatalf("answer = %q", got.Answer)
			}
			if fake.sent["model"] != tc.wantModel {
				t.Fatalf("model = %v, want %v", fake.sent["model"], tc.wantModel)
			}
			if fake.sent["stream"] != false {
				t.Fatalf("stream = %v, want false", fake.sent["stream"])
			}
			messages, _ := fake.sent["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages = %v", fake.sent["messages"])
			}
			system, _ := messages[0].(map[string]any)
			user, _ := messages[1].(map[string]any)
			if system["role"] != "system" || user["role"] != "user" || user["content"] != "Who sent it?" {
				t.Fatalf("messages = %v", messages)
			}
			prompt, _ := system["content"].(string)
			if !strings.Contains(prompt, "always answer in "+tc.wantLang+".") {
				t.Fatalf("system prompt %q does not pin %s", prompt, tc.wantLang)
			}
			hasMemory := strings.Contains(prompt, "- name: Ada\n- likes short answers")
			if hasMemory != tc.wantMemory {
				t.Fatalf("memories in prompt = %v, want %v: %q", hasMemory, tc.wantMemory, prompt)
			}
			if !tc.wantMemory && fake.searches != 0 {
				t.Fatalf("searched memories %d times", fake.searches)
			}
		})
	}
}

func TestMemoriesAvailableIsCached(t *testing.T) {
	checks := 0
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available":true}`))
	}))
	defer synaplan.Close()
	h, err := New(nil, synaplan.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if !h.memoriesAvailable(t.Context()) {
			t.Fatal("memories not available")
		}
	}
	if checks != 1 {
		t.Fatalf("checked %d times, want once", checks)
	}
}

func TestModelsListsAvailableChatModels(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		want       string
	}{
		{
			name:   "available chat models",
			status: http.StatusOK,
			body: `{"success":true,"models":{"CHAT":[
				{"id":1,"providerId":"gpt-4o","name":"GPT-4o","service":"OpenAI","available":true},
				{"id":2,"providerId":"off","name":"Off","service":"Groq","available":false},
				{"id":3,"providerId":"","name":"No id","service":"Groq","available":true}
			],"TEXT2PIC":[]}}`,
			wantStatus: http.StatusOK,
			want:       `{"models":[{"id":"gpt-4o","name":"GPT-4o","service":"OpenAI"}]}`,
		},
		{"none", http.StatusOK, `{"success":true}`, http.StatusOK, `{"models":[]}`},
		{"failing", http.StatusInternalServerError, `oops`, http.StatusBadGateway, `"code":"synaplan_error"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/config/models" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
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
			h.Models(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/models", nil))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("body = %s, want contains %s", rr.Body.String(), tc.want)
			}
		})
	}
}

func TestClipRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"äöüß", 2, "äö"},
		{"", 3, ""},
	}
	for _, tc := range cases {
		if got := clipRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("clipRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
