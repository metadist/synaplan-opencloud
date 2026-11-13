package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// chatMessageMaxLen bounds a question; chatModelMaxLen a model id.
const (
	chatMessageMaxLen = 8000
	chatModelMaxLen   = 200
)

// chatFileMaxRunes is how much of a file goes into one question. The
// model's context is the limit, not the file: a longer file is cut.
const chatFileMaxRunes = 20000

// answerLanguages are the languages the AI can be asked to answer in,
// with the English name the model instruction uses. Same list as the
// Nextcloud app.
var answerLanguages = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian",
	"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "tr": "Turkish", "ru": "Russian",
	"ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic",
}

const defaultAnswerLanguage = "en"

// errRateLimited is Synaplan saying the person asked too much.
var errRateLimited = fmt.Errorf("synaplan rate limit: %w", errClientInput)

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
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	req.Model = strings.TrimSpace(req.Model)
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "message is required"})
		return
	}
	if len(req.Message) > chatMessageMaxLen {
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

// fileText is the text of a file, read inline for text files and
// extracted by Synaplan for documents, cut to chatFileMaxRunes.
func (h *Handler) fileText(ctx context.Context, file *cs3reader.File) (string, error) {
	text, fileID, cleanup, err := h.prepareSummaryInput(ctx, file)
	defer cleanup()
	if err != nil {
		return "", err
	}
	if text != nil {
		return clipRunes(*text, chatFileMaxRunes), nil
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
	var body synaplanapi.PostOpenaiChatCompletionsJSONRequestBody
	body.Messages = slices.Grow(body.Messages, 2)[:2]
	system := synaplanapi.PostOpenaiChatCompletionsJSONBodyMessagesRoleSystem
	user := synaplanapi.PostOpenaiChatCompletionsJSONBodyMessagesRoleUser
	body.Messages[0].Role, body.Messages[0].Content = &system, h.systemPrompt(ctx, req.Language, req.Message)
	body.Messages[1].Role, body.Messages[1].Content = &user, content
	stream := false
	body.Stream = &stream
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

type chatModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
}

type modelsResponse struct {
	Models []chatModel `json:"models"`
}

// Models lists the chat models Synaplan offers this person. The id is
// what Chat passes on as the model.
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	res, err := h.synaplanAPI.GetApiConfigModelsListWithResponse(r.Context(), nil)
	if err != nil || res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		if err == nil {
			err = fmt.Errorf("config/models status %d: %s", res.StatusCode(), truncate(string(res.Body), 300))
		}
		log.Printf("models: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: "Synaplan could not finish this. Try again in a minute, or ask your administrator.",
			Code:  "synaplan_error",
		})
		return
	}
	out := modelsResponse{Models: []chatModel{}}
	if res.JSON200.Models != nil && res.JSON200.Models.CHAT != nil {
		for _, row := range *res.JSON200.Models.CHAT {
			if !row.Available || row.ProviderId == "" {
				continue
			}
			out.Models = append(out.Models, chatModel{ID: row.ProviderId, Name: row.Name, Service: row.Service})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// clipRunes cuts s to at most n runes without splitting one.
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
