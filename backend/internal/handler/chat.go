package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// chatMessageMaxLen bounds a question in Unicode characters; chatModelMaxLen bounds a model id.
const (
	chatMessageMaxLen = 8000
	chatModelMaxLen   = 200
)

// chatFileMaxRunes is how much of a file goes into one question. The
// model's context is the limit, not the file: a longer file is cut.
const chatFileMaxRunes = 20000

// defaultAnswerLanguage is the answer language when the person's OpenCloud
// language is not one answerLanguages lists. That list lives with the
// administrator's settings.
const defaultAnswerLanguage = "en"

type chatRequest struct {
	ResourceID string `json:"resourceId"`
	Message    string `json:"message"`
	Model      string `json:"model"`
	// Language is the person's OpenCloud interface language; the answer
	// comes in it when Synaplan knows it.
	Language string `json:"language"`
}

type chatResponse struct {
	Answer string `json:"answer"`
}

// Chat asks Synaplan one question, about a file when resourceId names
// one, otherwise in general. The answer is the finished text, not a
// stream: the dialog shows it when Synaplan is done.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	req.Model = strings.TrimSpace(req.Model)
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "message is required"})
		return
	}
	if utf8.RuneCountInString(req.Message) > chatMessageMaxLen {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("message must be %d characters or fewer", chatMessageMaxLen)})
		return
	}
	if len(req.Model) > chatModelMaxLen {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("model must be %d characters or fewer", chatModelMaxLen)})
		return
	}

	if req.ResourceID == "" {
		ctx, cancel := context.WithTimeout(r.Context(), summaryTimeout)
		defer cancel()
		answer, err := h.ask(ctx, req, "")
		if err != nil {
			writeFailure(w, "chat", err)
			return
		}
		writeJSON(w, http.StatusOK, chatResponse{Answer: answer})
		return
	}

	answer, ok := h.runSummaryPipeline(w, r, req.ResourceID, "chat", func(ctx context.Context, file *cs3reader.File) (string, error) {
		text, err := h.fileText(ctx, file)
		if err != nil {
			return "", err
		}
		return h.ask(ctx, req, text)
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, chatResponse{Answer: answer})
}

// fileText is the text of a file, cut to chatFileMaxRunes. Text files
// are read through a bounded UTF-8 prefix reader so a large file is
// never buffered whole. Documents go through Synaplan's extraction, and
// the extracted text is then cut on a rune boundary.
func (h *Handler) fileText(ctx context.Context, file *cs3reader.File) (string, error) {
	if readsAsText(file) {
		text, err := readRunePrefix(file.Body, chatFileMaxRunes)
		if err != nil {
			return "", fmt.Errorf("read file body: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", errFileEmpty
		}
		return text, nil
	}

	_, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
	defer cleanup()
	if err != nil {
		return "", err
	}
	if fileID == nil {
		return "", errNoText
	}
	res, err := h.synaplanAPI.GetApiFilesContentWithResponse(ctx, *fileID)
	if err != nil {
		return "", fmt.Errorf("files/content: %w", err)
	}
	if res.StatusCode() != http.StatusOK {
		return "", wrapUpstreamStatus("files/content", res.StatusCode(), res.Body)
	}
	// The pinned spec declares no schema for this response.
	var parsed struct {
		ExtractedText string `json:"extracted_text"`
	}
	if err := json.Unmarshal(res.Body, &parsed); err != nil {
		return "", fmt.Errorf("parse files/content response: %w", err)
	}
	if strings.TrimSpace(parsed.ExtractedText) == "" {
		return "", errNoText
	}
	return clipRunes(parsed.ExtractedText, chatFileMaxRunes), nil
}

// ask sends one question, with the file's text when there is one, to
// Synaplan's chat completion.
func (h *Handler) ask(ctx context.Context, req chatRequest, fileText string) (string, error) {
	content := req.Message
	if fileText != "" {
		content = "The file says:\n\n" + fileText + "\n\nQuestion: " + req.Message
	}
	system := synaplanapi.PostOpenaiChatCompletionsJSONBodyMessagesRoleSystem
	user := synaplanapi.PostOpenaiChatCompletionsJSONBodyMessagesRoleUser
	stream := false
	var sysMsg, userMsg struct {
		Content    any                                                        `json:"content,omitempty"`
		Role       *synaplanapi.PostOpenaiChatCompletionsJSONBodyMessagesRole `json:"role,omitempty"`
		ToolCallId *string                                                    `json:"tool_call_id,omitempty"`
		ToolCalls  *[]map[string]any                                          `json:"tool_calls,omitempty"`
	}
	sysMsg.Role, sysMsg.Content = &system, h.systemPrompt(ctx, req.Language, req.Message)
	userMsg.Role, userMsg.Content = &user, content
	body := synaplanapi.PostOpenaiChatCompletionsJSONRequestBody{
		Messages: append(synaplanapi.PostOpenaiChatCompletionsJSONRequestBody{}.Messages, sysMsg, userMsg),
		Stream:   &stream,
	}
	if req.Model != "" {
		body.Model = &req.Model
	}

	res, err := h.synaplanAPI.PostOpenaiChatCompletionsWithResponse(ctx, body)
	if err != nil {
		return "", fmt.Errorf("chat/completions: %w", err)
	}
	if res.StatusCode() == http.StatusTooManyRequests {
		return "", errRateLimited
	}
	if res.StatusCode() != http.StatusOK {
		return "", wrapUpstreamStatus("chat/completions", res.StatusCode(), res.Body)
	}
	if res.JSON200 == nil || res.JSON200.Choices == nil || len(*res.JSON200.Choices) == 0 {
		return "", errors.New("chat/completions returned no choice")
	}
	message := (*res.JSON200.Choices)[0].Message
	if message == nil || message.Content == nil || strings.TrimSpace(*message.Content) == "" {
		return "", errors.New("chat/completions returned an empty answer")
	}
	return *message.Content, nil
}

// systemPrompt pins the answer language to the person's interface
// language and adds what Synaplan remembers about them. The same two
// rules as the Nextcloud app.
func (h *Handler) systemPrompt(ctx context.Context, uiLanguage, question string) string {
	parts := []string{
		"You are Synaplan AI, a helpful assistant integrated into OpenCloud.",
		"Unless the user explicitly asks for a different language, always answer in " + answerLanguages[answerLanguage(uiLanguage)] + ".",
	}
	if memories := h.memoryContext(ctx, question); memories != "" {
		parts = append(parts, "Here is what you remember about the user. Use it when relevant, but do not mention that you are reading from memory:\n"+memories)
	}
	return strings.Join(parts, "\n\n")
}

// answerLanguage maps a locale ("pt_BR", "DE") to a language the answer
// can come in, English when there is none.
func answerLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) >= 2 {
		if _, ok := answerLanguages[raw[:2]]; ok {
			return raw[:2]
		}
	}
	return defaultAnswerLanguage
}

// clipRunes cuts s to at most n runes without splitting one. It walks
// the string in place, so the whole text is not copied into a rune slice.
func clipRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	i := 0
	for runes := 0; runes < n && i < len(s); runes++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}

// readRunePrefix reads at most maxRunes runes from r in bounded chunks
// and stops on a rune boundary without buffering the rest of the stream.
// Any non-EOF read error encountered while reading up to maxRunes is
// returned immediately so a truncated download is never passed on as
// complete text.
func readRunePrefix(r io.Reader, maxRunes int) (string, error) {
	if maxRunes <= 0 {
		return "", nil
	}
	var out strings.Builder
	out.Grow(min(maxRunes, 4096))
	var buf [4096]byte
	var pending []byte
	runes := 0
	for runes < maxRunes {
		need := min(len(buf), (maxRunes-runes)*utf8.UTFMax)
		n, err := r.Read(buf[:need])
		if n > 0 {
			pending = append(pending, buf[:n]...)
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		for runes < maxRunes && len(pending) > 0 {
			if !utf8.FullRune(pending) && err != io.EOF {
				break
			}
			ru, size := utf8.DecodeRune(pending)
			out.WriteRune(ru)
			pending = pending[size:]
			runes++
		}
		if err == io.EOF {
			break
		}
		if n == 0 {
			return "", io.ErrNoProgress
		}
	}
	return out.String(), nil
}
