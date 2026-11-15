package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		{strings.Repeat("文", 5), 3, strings.Repeat("文", 3)},
	}
	for _, tc := range cases {
		if got := clipRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("clipRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestChatMessageLimitCountsRunes(t *testing.T) {
	// "文" is one character and three bytes. Three thousand of them sit
	// under the character limit and over the old byte limit.
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
			message := strings.Repeat(han, tc.runes)
			if tc.runes == 3000 && len(message) <= chatMessageMaxLen {
				t.Fatal("fixture must be longer in bytes than the character limit")
			}
			var sent map[string]any
			synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/chat/completions":
					body, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(body, &sent); err != nil {
						t.Errorf("completion body: %v", err)
					}
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
				case "/api/v1/config/memory-service/check":
					_, _ = w.Write([]byte(`{"available":false}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(synaplan.Close)
			h, err := New(nil, synaplan.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]string{"message": message})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			h.Chat(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/chat", strings.NewReader(string(payload))))
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d, body %s", rr.Code, tc.want, rr.Body.String())
			}
			if tc.want != http.StatusOK {
				if !strings.Contains(rr.Body.String(), "8000 characters") {
					t.Fatalf("body = %q, want the character limit", rr.Body.String())
				}
				if sent != nil {
					t.Fatal("upstream was called for a too-long question")
				}
				return
			}
			messages, _ := sent["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages = %v", sent["messages"])
			}
			user, _ := messages[1].(map[string]any)
			if user["content"] != message {
				t.Fatalf("question was not accepted whole (%d runes)", tc.runes)
			}
		})
	}
}

// capReader serves prefix and then either fails or yields tail. extra
// is how many tail bytes a caller actually pulled.
type capReader struct {
	prefix []byte
	tail   []byte
	off    int
	past   int
	extra  int
}

var errPastCap = errors.New("read past cap")

func (c *capReader) Read(p []byte) (int, error) {
	if c.off < len(c.prefix) {
		n := copy(p, c.prefix[c.off:])
		c.off += n
		return n, nil
	}
	c.past++
	if len(c.tail) == 0 {
		return 0, errPastCap
	}
	n := copy(p, c.tail)
	c.tail = c.tail[n:]
	c.extra += n
	c.off += n
	if len(c.tail) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func TestReadRunePrefixDoesNotReadPastCap(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"ascii", strings.Repeat("a", 64)},
		{"two byte runes", strings.Repeat("ä", 32)},
		{"han", strings.Repeat("文", 20)},
		{"four byte rune", "𐍈"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &capReader{prefix: []byte(tc.prefix)}
			got, err := readRunePrefix(r, len([]rune(tc.prefix)))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.prefix {
				t.Fatalf("got %q, want %q", got, tc.prefix)
			}
			if r.past != 0 || r.off != len(tc.prefix) {
				t.Fatalf("off=%d past=%d, want off=%d and no read past the cap", r.off, r.past, len(tc.prefix))
			}
		})
	}
}

type byteAndError struct {
	b   byte
	err error
	did bool
}

func (r *byteAndError) Read(p []byte) (int, error) {
	if r.did {
		return 0, io.EOF
	}
	r.did = true
	p[0] = r.b
	return 1, r.err
}

func TestReadRunePrefixKeepsTheErrorThatArrivesWithAByte(t *testing.T) {
	_, err := readRunePrefix(&byteAndError{b: 'A', err: io.ErrUnexpectedEOF}, 8)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want unexpected EOF", err)
	}
}

func TestReadRunePrefixReturnsEarlyReadError(t *testing.T) {
	_, err := readRunePrefix(&capReader{}, 8)
	if !errors.Is(err, errPastCap) {
		t.Fatalf("err = %v, want past-cap", err)
	}
}

func TestFileTextDoesNotReadPastRuneCap(t *testing.T) {
	prefix := strings.Repeat("ä", chatFileMaxRunes)
	tail := strings.Repeat("Z", 1<<20)
	r := &capReader{prefix: []byte(prefix), tail: []byte(tail)}
	got, err := (&Handler{}).fileText(t.Context(), &cs3reader.File{
		MimeType: "text/plain",
		Body:     io.NopCloser(r),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != prefix {
		t.Fatalf("got len %d, want prefix len %d", len(got), len(prefix))
	}
	if strings.Contains(got, "Z") {
		t.Fatal("tail was retained in the text")
	}
	if r.extra != 0 || r.past != 0 {
		t.Fatalf("read %d extra bytes in %d reads past the cap", r.extra, r.past)
	}
}

func TestFileTextRejectsEmptyText(t *testing.T) {
	_, err := (&Handler{}).fileText(t.Context(), &cs3reader.File{
		MimeType: "text/plain",
		Body:     io.NopCloser(strings.NewReader(" \n\t")),
	})
	if !errors.Is(err, errFileEmpty) {
		t.Fatalf("err = %v, want empty file", err)
	}
}

func TestChatAsksWhenMemorySearchHangs(t *testing.T) {
	var logs strings.Builder
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	searched := make(chan struct{}, 1)
	synaplan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/config/memory-service/check":
			_, _ = w.Write([]byte(`{"available":true}`))
		case "/api/v1/user/memories/search":
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case searched <- struct{}{}:
			default:
			}
			// Hang until the client gives up. The timer only unblocks
			// the handler if cancellation never arrives, so Close cannot
			// wait forever.
			timer := time.NewTimer(memorySearchTimeout + 30*time.Second)
			select {
			case <-r.Context().Done():
			case <-timer.C:
				t.Errorf("memory search was not cancelled when its deadline passed")
			}
			timer.Stop()
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"still here"}}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(synaplan.Close)

	h, err := New(nil, synaplan.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	started := time.Now()
	h.Chat(rr, httptest.NewRequest(http.MethodPost, "/api/synaplan/chat", strings.NewReader(`{"message":"Who sent it?"}`)))
	elapsed := time.Since(started)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, log %s", rr.Code, rr.Body.String(), logs.String())
	}
	if !strings.Contains(rr.Body.String(), "still here") {
		t.Fatalf("body = %s", rr.Body.String())
	}
	select {
	case <-searched:
	default:
		t.Fatal("memory search was not called")
	}
	if elapsed < memorySearchTimeout/2 {
		t.Fatalf("returned in %s, before the memory search deadline", elapsed)
	}
	if elapsed > memorySearchTimeout+20*time.Second {
		t.Fatalf("returned in %s; a stalled memory search held the question", elapsed)
	}
	if !strings.Contains(logs.String(), "memory search:") || !strings.Contains(logs.String(), "deadline") {
		t.Fatalf("log = %q, want the memory search deadline", logs.String())
	}
}
