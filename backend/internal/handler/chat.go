package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type chatRequest struct {
	ResourceID string `json:"resourceId"`
	Message    string `json:"message"`
	Model      string `json:"model"`
	GroupKey   string `json:"groupKey"`
	// Language is the person's OpenCloud interface language. Whether it
	// decides the answer language is the administrator's choice.
	Language string `json:"language"`
}

// Chat asks Synaplan about a file, or answers a question from the
// Synaplan page. The answer is the finished text, not a stream: the
// dialog shows it when Synaplan is done.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Type a question."})
		return
	}
	content := req.Message
	if req.ResourceID != "" {
		excerpt, fail := h.fileExcerpt(r, req.ResourceID)
		if fail != nil {
			writeJSON(w, fail.status, errorResponse{Error: fail.sentence, Code: fail.code})
			return
		}
		content = "The file says:\n\n" + excerpt + "\n\nQuestion: " + req.Message
	}
	system := h.systemPrompt(r.Context(), req.Language, req.Message)
	answer, code, err := h.complete(r.Context(), req.Model, system, content, req.GroupKey)
	if err != nil {
		log.Printf("chat: %v", err)
		status := http.StatusBadGateway
		sentence := "Synaplan could not finish this. Try again in a minute, or ask your administrator."
		kind := "synaplan_error"
		if code == http.StatusTooManyRequests {
			status = http.StatusTooManyRequests
			sentence = "Synaplan is busy or your limit is reached. Try again later."
			kind = "rate_limited"
		}
		writeJSON(w, status, errorResponse{Error: sentence, Code: kind})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"answer": answer})
}

type chatFail struct {
	status   int
	sentence string
	code     string
}

func (h *Handler) fileExcerpt(r *http.Request, resourceID string) (string, *chatFail) {
	if h.cs3 == nil {
		return "", &chatFail{http.StatusBadGateway, "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", "synaplan_unreachable"}
	}
	ctx := r.Context()
	file, err := h.cs3.Open(ctx, resourceID)
	if err != nil {
		log.Printf("chat open: %v", err)
		return "", &chatFail{http.StatusBadGateway, "This file is no longer there. Reload the folder and try again.", "file_not_found"}
	}
	defer func() { _ = file.Body.Close() }()
	text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
	defer cleanup()
	if err != nil {
		log.Printf("chat prepare: %v", err)
		failure := publicFailure(err)
		return "", &chatFail{http.StatusUnprocessableEntity, failure.Error, failure.Code}
	}
	if text != nil {
		return clip(*text, 20000), nil
	}
	if fileID == nil {
		return "", &chatFail{http.StatusUnprocessableEntity, "Synaplan found no text in this file. It may be a scan or protected by a password.", "no_text"}
	}
	status, body, err := h.synaplanCall(ctx, http.MethodGet, "/api/v1/files/"+strconv.Itoa(*fileID)+"/content", nil)
	if err != nil || status != http.StatusOK {
		log.Printf("chat content: status %d err %v", status, err)
		return "", &chatFail{http.StatusBadGateway, "Synaplan could not finish this. Try again in a minute, or ask your administrator.", "synaplan_error"}
	}
	var parsed struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(body, &parsed)
	got := parsed.Text
	if got == "" {
		got = parsed.Content
	}
	if strings.TrimSpace(got) == "" {
		return "", &chatFail{http.StatusUnprocessableEntity, "Synaplan found no text in this file. It may be a scan or protected by a password.", "no_text"}
	}
	return clip(got, 20000), nil
}

// systemPrompt pins the answer language the administrator chose (or the
// person's own) and adds what Synaplan remembers about the person when
// memories are allowed. The same two rules as the Nextcloud app.
func (h *Handler) systemPrompt(ctx context.Context, uiLanguage, question string) string {
	parts := []string{
		"You are Synaplan AI, a helpful assistant integrated into OpenCloud.",
		"Unless the user explicitly asks for a different language, always answer in " + languageName(h.resolveLanguage(uiLanguage)) + ".",
	}
	if memories := h.memoryContext(ctx, question); memories != "" {
		parts = append(parts, "Here is what you remember about the user. Use it when relevant, but do not mention that you are reading from memory:\n"+memories)
	}
	return strings.Join(parts, "\n\n")
}

func (h *Handler) complete(ctx context.Context, model, system, content, group string) (string, int, error) {
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": content},
		},
		"stream": false,
	}
	if model != "" {
		payload["model"] = model
	}
	if group != "" {
		payload["group_key"] = group
	}
	status, body, err := h.synaplanCall(ctx, http.MethodPost, "/v1/chat/completions", payload)
	if err != nil {
		return "", 0, err
	}
	if status != http.StatusOK {
		return "", status, errStatus(status)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", status, err
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", status, errStatus(status)
	}
	return parsed.Choices[0].Message.Content, status, nil
}

// Models lists the chat models Synaplan offers this person.
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	status, body, err := h.synaplanCall(r.Context(), http.MethodGet, "/api/v1/config/models", nil)
	if err != nil {
		log.Printf("models: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan cannot be reached. Try again in a minute, or ask your administrator.", Code: "synaplan_unreachable"})
		return
	}
	writeRaw(w, status, body)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type statusError struct{ code int }

func (e statusError) Error() string { return "synaplan status" }

func errStatus(code int) error { return statusError{code: code} }
