package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/settings"
)

// adminPermission is the OpenCloud permission that makes a person an
// administrator here. Only OpenCloud's Admin role holds it by default, and
// OpenCloud's own Graph API asks for it before it lets anyone manage users.
const adminPermission = "Accounts.ReadWrite"

// answerLanguages are the languages the AI can be asked to answer in, with
// the English name the page shows. Same list as the Nextcloud app.
var answerLanguages = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian",
	"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "tr": "Turkish", "ru": "Russian",
	"ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic",
}

// fallbackAnswerLanguage is used when neither the store nor the
// configuration names a supported language.
const fallbackAnswerLanguage = "en"

// normalizeLanguage maps a locale ("pt_BR", "DE") to a supported code, or "".
func normalizeLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) < 2 {
		return ""
	}
	if _, ok := answerLanguages[raw[:2]]; ok {
		return raw[:2]
	}
	return ""
}

// isAdmin reports whether the signed-in person holds adminPermission. When
// OpenCloud cannot answer, nobody is an administrator.
func (h *Handler) isAdmin(ctx context.Context) bool {
	if h.permissions == nil {
		return false
	}
	admin, err := h.permissions.Has(ctx, adminPermission)
	if err != nil {
		log.Printf("admin check: %v", err)
		return false
	}
	return admin
}

// adminSettings is the configuration in force: what the administrator
// saved, or else the service configuration. When the store cannot be read,
// the service configuration applies, so a NATS outage does not stop
// summaries.
func (h *Handler) adminSettings(ctx context.Context) settings.Values {
	values := h.settingsDefaults
	if h.settings != nil {
		stored, ok, err := h.settings.Get(ctx)
		if err != nil {
			log.Printf("admin settings: %v", err)
		} else if ok {
			values = stored
		}
	}
	values.DefaultAnswerLanguage = normalizeLanguage(values.DefaultAnswerLanguage)
	if values.DefaultAnswerLanguage == "" {
		values.DefaultAnswerLanguage = fallbackAnswerLanguage
	}
	return values
}

// resolveLanguage is the language one answer comes in: the person's
// interface language when the administrator allows it and it is supported,
// otherwise the default.
func (h *Handler) resolveLanguage(ctx context.Context, uiLanguage string) string {
	values := h.adminSettings(ctx)
	if values.UseUserLanguage {
		if code := normalizeLanguage(uiLanguage); code != "" {
			return code
		}
	}
	return values.DefaultAnswerLanguage
}

// connectionMode names how people here reach Synaplan, with the names the
// Nextcloud app uses for the same setups.
func (h *Handler) connectionMode() string {
	if h.sharedKey {
		return "shared"
	}
	return "organisation"
}

type languageOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type settingsResponse struct {
	Mode                  string           `json:"mode"`
	IsAdmin               bool             `json:"isAdmin"`
	DefaultAnswerLanguage string           `json:"defaultAnswerLanguage"`
	UseUserLanguage       bool             `json:"useUserLanguage"`
	Languages             []languageOption `json:"languages"`
}

type saveSettingsRequest struct {
	DefaultAnswerLanguage string `json:"defaultAnswerLanguage"`
	UseUserLanguage       bool   `json:"useUserLanguage"`
}

// Settings answers what everyone may know: the connection mode and the
// answer language in force, and whether the signed-in person may change it.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	values := h.adminSettings(r.Context())
	codes := make([]string, 0, len(answerLanguages))
	for code := range answerLanguages {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	languages := make([]languageOption, 0, len(codes))
	for _, code := range codes {
		languages = append(languages, languageOption{Code: code, Name: answerLanguages[code]})
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		Mode:                  h.connectionMode(),
		IsAdmin:               h.isAdmin(r.Context()),
		DefaultAnswerLanguage: values.DefaultAnswerLanguage,
		UseUserLanguage:       values.UseUserLanguage,
		Languages:             languages,
	})
}

// SaveSettings stores the administrator's choices. Anyone else gets 403.
func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	var req saveSettingsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	code := normalizeLanguage(req.DefaultAnswerLanguage)
	if code == "" || code != strings.ToLower(strings.TrimSpace(req.DefaultAnswerLanguage)) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose one of the listed languages.", Code: "unsupported_language"})
		return
	}
	if !h.isAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "Only an administrator can change this.", Code: "admin_only"})
		return
	}
	if h.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "The settings were not saved. Try again in a minute.", Code: "settings_unavailable"})
		return
	}
	if err := h.settings.Put(r.Context(), settings.Values{DefaultAnswerLanguage: code, UseUserLanguage: req.UseUserLanguage}); err != nil {
		log.Printf("save admin settings: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "The settings were not saved. Try again in a minute.", Code: "settings_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}
