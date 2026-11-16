package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
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
		sharedKey  bool
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
			sharedKey:  true,
			wantStatus: http.StatusOK,
			wantLang:   "English",
		},
		{
			name:       "rate limited",
			body:       `{"message":"Who sent it?"}`,
			fake:       fakeChatSynaplan{completionStatus: http.StatusTooManyRequests, completionBody: `{}`},
			wantStatus: http.StatusTooManyRequests,
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
			if tc.sharedKey {
				h.UseSharedKey()
			}

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

func TestChatMessageLimitCountsRunes(t *testing.T) {
	const han = "文"
	cases := []struct {
		name  string
		runes int
		want  int
	}{
		{"three thousand han characters", 3000, http.StatusOK},
		{"exactly the character limit", chatMessageMaxLen, http.StatusOK},
		{"one character over the limit", chatMessageMaxLen + 1, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeChatSynaplan{
				completionStatus: http.StatusOK,
				completionBody:   `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
			}
			synaplan := httptest.NewServer(fake.handler(t))
			defer synaplan.Close()
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(chatRequest{Message: strings.Repeat(han, tc.runes)})
			rr := httptest.NewRecorder()
			h.Chat(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/chat", strings.NewReader(string(payload))))
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

type failingTailReader struct {
	prefix string
	read   int
	pulled bool
}

func (r *failingTailReader) Read(p []byte) (int, error) {
	if r.read >= len(r.prefix) {
		r.pulled = true
		return 0, errors.New("tail past rune limit was read")
	}
	n := copy(p, r.prefix[r.read:])
	r.read += n
	return n, nil
}

type byteWithErrReader struct{}

func (byteWithErrReader) Read(p []byte) (int, error) {
	p[0] = 'A'
	return 1, io.ErrUnexpectedEOF
}

func TestFileTextBoundsTextReadsAndPreservesErrors(t *testing.T) {
	t.Run("stops at chatFileMaxRunes without reading the tail", func(t *testing.T) {
		prefix := strings.Repeat("ä", chatFileMaxRunes)
		tail := &failingTailReader{prefix: prefix}
		h := &Handler{}
		got, err := h.fileText(context.Background(), &cs3reader.File{
			MimeType: "text/plain",
			Body:     io.NopCloser(tail),
		})
		if err != nil {
			t.Fatal(err)
		}
		if utf8.RuneCountInString(got) != chatFileMaxRunes || tail.pulled {
			t.Fatalf("runes = %d, pulled tail = %v", utf8.RuneCountInString(got), tail.pulled)
		}
	})

	t.Run("preserves an error returned with the last byte", func(t *testing.T) {
		h := &Handler{}
		_, err := h.fileText(context.Background(), &cs3reader.File{
			MimeType: "text/plain",
			Body:     io.NopCloser(byteWithErrReader{}),
		})
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
}

func TestMemoriesAvailableCachesDefinitiveAnswersAndRetriesTransientFailures(t *testing.T) {
	var checks atomic.Int32
	var failCheck atomic.Bool
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		checks.Add(1)
		if failCheck.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available":true}`))
	}))
	defer synaplan.Close()
	h, err := New(nil, synaplan.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	failCheck.Store(true)
	if h.memoriesAvailable(t.Context()) {
		t.Fatal("expected false on failed check")
	}
	// Reset retry window to simulate memoryRetryAfter elapsed (much shorter than 5m).
	h.memory.mu.Lock()
	if time.Until(h.memory.until) > 30*time.Second {
		t.Fatalf("transient failure cached for %s, want ~15s retry", time.Until(h.memory.until))
	}
	h.memory.until = time.Time{}
	h.memory.mu.Unlock()

	failCheck.Store(false)
	for range 3 {
		if !h.memoriesAvailable(t.Context()) {
			t.Fatal("memories not available")
		}
	}
	if checks.Load() != 2 {
		t.Fatalf("checked %d times, want 2 (one failed, one cached success)", checks.Load())
	}
}

func TestStalledMemorySearchFallsBackWithoutBlockingChat(t *testing.T) {
	original := memorySearchTimeout
	memorySearchTimeout = 100 * time.Millisecond
	t.Cleanup(func() { memorySearchTimeout = original })

	unblock := make(chan struct{})
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/config/memory-service/check":
			_, _ = w.Write([]byte(`{"available":true}`))
		case "/api/v1/user/memories/search":
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Still works"}}]}`))
		}
	}))
	t.Cleanup(synaplan.Close)
	t.Cleanup(func() { close(unblock) })
	h, err := New(nil, synaplan.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/synaplan/chat", strings.NewReader(`{"message":"Hello"}`))
	rr := httptest.NewRecorder()
	h.Chat(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Still works") {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
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
		{"rate limited", http.StatusTooManyRequests, `{}`, http.StatusTooManyRequests, `"code":"rate_limited"`},
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
		{strings.Repeat("文", 5), 3, strings.Repeat("文", 3)},
	}
	for _, tc := range cases {
		if got := clipRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("clipRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
