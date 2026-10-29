package handler

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencloud-eu/opencloud/pkg/roles"

	"github.com/metadist/synaplan-opencloud/internal/state"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// adminRoleID is OpenCloud's built-in Admin role
// (services/settings/pkg/store/defaults.BundleUUIDRoleAdmin). The
// proxy puts a person's role ids on the request context.
const adminRoleID = "71881883-1768-46bd-a24d-a356a2afdf7f"

// answerLanguages are the languages the AI can be asked to answer in,
// with the English name the model instruction uses. Same list as the
// Nextcloud app.
var answerLanguages = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian",
	"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "tr": "Turkish", "ru": "Russian",
	"ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic",
}

const defaultAnswerLanguage = "en"

// isAdmin reports whether the signed-in person holds OpenCloud's Admin
// role. The role ids on the request context are used when the proxy put
// them there; otherwise OpenCloud's Graph API is asked with the person's
// own token, and the answer is kept for five minutes.
func (h *Handler) isAdmin(ctx context.Context) bool {
	if ids, ok := roles.ReadRoleIDsFromContext(ctx); ok {
		return hasAdminRole(ids)
	}
	uid := currentUserID(ctx)
	if uid == "" {
		return false
	}
	h.adminMu.Lock()
	cached, ok := h.adminCache[uid]
	h.adminMu.Unlock()
	if ok && time.Since(cached.at) < 5*time.Minute {
		return cached.admin
	}
	admin := h.graphSaysAdmin(ctx)
	h.adminMu.Lock()
	if h.adminCache == nil {
		h.adminCache = map[string]adminAnswer{}
	}
	h.adminCache[uid] = adminAnswer{admin: admin, at: time.Now()}
	h.adminMu.Unlock()
	return admin
}

func hasAdminRole(ids []string) bool {
	for _, id := range ids {
		if id == adminRoleID {
			return true
		}
	}
	return false
}

// graphSaysAdmin asks OpenCloud's Graph API for the person's role
// assignments with the token OpenCloud itself issued them.
func (h *Handler) graphSaysAdmin(ctx context.Context) bool {
	base := strings.TrimRight(h.settings.OpenCloudURL, "/")
	token := synaplanauth.OIDCTokenFromContext(ctx)
	if base == "" || token == "" {
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, base+"/graph/v1.0/me?$expand=appRoleAssignments", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := h.openCloudClient().Do(req)
	if err != nil {
		log.Printf("graph role lookup: %v", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var me struct {
		AppRoleAssignments []struct {
			AppRoleID string `json:"appRoleId"`
		} `json:"appRoleAssignments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me); err != nil {
		return false
	}
	ids := make([]string, 0, len(me.AppRoleAssignments))
	for _, assignment := range me.AppRoleAssignments {
		ids = append(ids, assignment.AppRoleID)
	}
	return hasAdminRole(ids)
}

// openCloudClient talks to OpenCloud's own API. In dev that is a
// self-signed certificate, which Insecure accepts.
func (h *Handler) openCloudClient() *http.Client {
	h.adminMu.Lock()
	defer h.adminMu.Unlock()
	if h.ocClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if h.settings.Insecure {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // dev stacks use a self-signed certificate
		}
		h.ocClient = &http.Client{Transport: transport, Timeout: 15 * time.Second}
	}
	return h.ocClient
}

type adminAnswer struct {
	admin bool
	at    time.Time
}

// normalizeLanguage maps a locale ("pt_BR", "DE") to a supported code, or "".
func normalizeLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) < 2 {
		return ""
	}
	short := raw[:2]
	if _, ok := answerLanguages[short]; ok {
		return short
	}
	return ""
}

// adminSettings is the effective configuration: the administrator's
// stored choices over the service configuration.
type adminSettings struct {
	DefaultAnswerLanguage string
	UseUserLanguage       bool
	AllowMemories         bool
}

func (h *Handler) adminSettings() adminSettings {
	out := adminSettings{
		DefaultAnswerLanguage: normalizeLanguage(h.settings.DefaultAnswerLanguage),
		UseUserLanguage:       h.settings.UseUserLanguage,
		AllowMemories:         h.settings.AllowMemories,
	}
	if out.DefaultAnswerLanguage == "" {
		out.DefaultAnswerLanguage = defaultAnswerLanguage
	}
	stored := h.store().Admin()
	if code := normalizeLanguage(stored.DefaultAnswerLanguage); code != "" {
		out.DefaultAnswerLanguage = code
	}
	if stored.UseUserLanguage != nil {
		out.UseUserLanguage = *stored.UseUserLanguage
	}
	if stored.AllowMemories != nil {
		out.AllowMemories = *stored.AllowMemories
	}
	return out
}

// resolveLanguage is the language one answer comes in: the person's
// interface language when the administrator allows it and it is
// supported, otherwise the default.
func (h *Handler) resolveLanguage(uiLanguage string) string {
	settings := h.adminSettings()
	if settings.UseUserLanguage {
		if code := normalizeLanguage(uiLanguage); code != "" {
			return code
		}
	}
	return settings.DefaultAnswerLanguage
}

// languageName is the English name used in a model instruction.
func languageName(code string) string {
	if name, ok := answerLanguages[code]; ok {
		return name
	}
	return answerLanguages[defaultAnswerLanguage]
}

// connectionMode is the name the Nextcloud app uses for the same setup.
func (h *Handler) connectionMode() string {
	if h.settings.SharedKey {
		return "shared"
	}
	return "organisation"
}

type settingsResponse struct {
	Mode                  string           `json:"mode"`
	IsAdmin               bool             `json:"isAdmin"`
	DefaultAnswerLanguage string           `json:"defaultAnswerLanguage"`
	UseUserLanguage       bool             `json:"useUserLanguage"`
	AllowMemories         bool             `json:"allowMemories"`
	MemoriesAvailable     bool             `json:"memoriesAvailable"`
	Languages             []languageOption `json:"languages"`
}

type languageOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Settings answers what everyone may know: the connection mode and the
// answer language in force. The administrator also sees whether the
// memory service is reachable.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	settings := h.adminSettings()
	admin := h.isAdmin(r.Context())
	codes := make([]string, 0, len(answerLanguages))
	for code := range answerLanguages {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	languages := make([]languageOption, 0, len(codes))
	for _, code := range codes {
		languages = append(languages, languageOption{Code: code, Name: answerLanguages[code]})
	}
	available := false
	if admin || settings.AllowMemories {
		available = h.memoriesAvailable(r.Context())
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		Mode:                  h.connectionMode(),
		IsAdmin:               admin,
		DefaultAnswerLanguage: settings.DefaultAnswerLanguage,
		UseUserLanguage:       settings.UseUserLanguage,
		AllowMemories:         settings.AllowMemories,
		MemoriesAvailable:     available,
		Languages:             languages,
	})
}

// SaveSettings stores the administrator's choices. Anyone else gets 403.
func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "Only an administrator can change this.", Code: "admin_only"})
		return
	}
	var req struct {
		DefaultAnswerLanguage string `json:"defaultAnswerLanguage"`
		UseUserLanguage       bool   `json:"useUserLanguage"`
		AllowMemories         bool   `json:"allowMemories"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	code := normalizeLanguage(req.DefaultAnswerLanguage)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose one of the listed languages."})
		return
	}
	useUser := req.UseUserLanguage
	allow := req.AllowMemories
	if err := h.store().PutAdmin(state.Admin{
		DefaultAnswerLanguage: code,
		UseUserLanguage:       &useUser,
		AllowMemories:         &allow,
	}); err != nil {
		log.Printf("save admin settings: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "The settings were not saved. Try again in a minute.", Code: "synaplan_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// memoriesAvailable asks Synaplan once every five minutes whether its
// memory service answers. A failed check counts as unavailable.
// memoryCheckTimeout bounds the memory-service health check.
const memoryCheckTimeout = 5 * time.Second

func (h *Handler) memoriesAvailable(ctx context.Context) bool {
	h.memoryMu.Lock()
	defer h.memoryMu.Unlock()
	if time.Since(h.memoryAt) < 5*time.Minute {
		return h.memoryOK
	}
	// Under the lock: a short deadline of its own, so one slow health
	// check cannot hold every later settings read and chat.
	checkCtx, cancel := context.WithTimeout(ctx, memoryCheckTimeout)
	defer cancel()
	status, body, err := h.synaplanCall(checkCtx, http.MethodGet, "/api/v1/config/memory-service/check", nil)
	available := false
	if err == nil && status == http.StatusOK {
		var parsed struct {
			Available bool `json:"available"`
		}
		_ = json.Unmarshal(body, &parsed)
		available = parsed.Available
	}
	h.memoryOK = available
	h.memoryAt = time.Now()
	return available
}

// memoryContext is what Synaplan remembers about the person, as lines
// for the model. Empty when memories are off, unavailable, or absent.
func (h *Handler) memoryContext(ctx context.Context, question string) string {
	if !h.adminSettings().AllowMemories || strings.TrimSpace(question) == "" || !h.memoriesAvailable(ctx) {
		return ""
	}
	status, body, err := h.synaplanCall(ctx, http.MethodPost, "/api/v1/user/memories/search", map[string]any{
		"query": question,
		"limit": 6,
	})
	if err != nil || status != http.StatusOK {
		return ""
	}
	var parsed struct {
		Memories []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"memories"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	lines := make([]string, 0, len(parsed.Memories))
	for _, memory := range parsed.Memories {
		value := strings.TrimSpace(memory.Value)
		if value == "" {
			continue
		}
		if key := strings.TrimSpace(memory.Key); key != "" {
			lines = append(lines, "- "+key+": "+value)
		} else {
			lines = append(lines, "- "+value)
		}
	}
	return strings.Join(lines, "\n")
}

// memoryCache guards the five-minute memory-service answer and the
// per-person administrator answer.
type memoryCache struct {
	memoryMu   sync.Mutex
	memoryOK   bool
	memoryAt   time.Time
	adminMu    sync.Mutex
	adminCache map[string]adminAnswer
	ocClient   *http.Client
}
