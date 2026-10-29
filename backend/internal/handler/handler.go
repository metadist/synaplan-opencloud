// Package handler hosts the HTTP handlers for the synaplan-opencloud
// backend. Each exposed endpoint lives in its own file (me.go,
// translate.go, ...) and shares the Handler struct defined here.
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/notify"
	"github.com/metadist/synaplan-opencloud/internal/state"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// Handler holds the state shared by all endpoints:
//
//   - synaplanAPI is the generated Synaplan client with an outbound
//     auth editor registered (either OIDC token exchange or a shared
//     API key, picked at startup in pkg/command/server.go), so every
//     call is authenticated without per-handler boilerplate.
//   - cs3 reads file bytes from OpenCloud storage via the reva
//     gateway for file-based operations like /translate.
//   - assetProxy streams whitelisted brand assets from the paired
//     Synaplan install for the public /api/synaplan/assets endpoint.
//
// The Synaplan API client has no global HTTP timeout — per-call
// timeouts are set on the request context instead. /me uses the
// incoming request's context (cancelled when the client disconnects);
// /translate sets a generous context.WithTimeout because LLM calls
// can take minutes.
// Settings is the cloud-file configuration the process was started with.
type Settings struct {
	InstanceID     string
	InstanceSecret string
	AdminAPIKey    string
	DeliveryURL    string
	PublicURL      string
	ServerID       string
	OIDCClientID   string
	MachineAuthKey string
	StatePath      string
	StateKey       string
	// HoldInbound accepts a verified notification and does not fetch it.
	// Tests use it. It is never set from the UI.
	HoldInbound bool
	// AllowSelfWrite lets a watch upload a file this service just wrote.
	AllowSelfWrite bool
	// SharedKey is true when every call rides on one API key. Shown to the
	// administrator as "One shared account".
	SharedKey bool
	// OpenCloudURL is OpenCloud's own API, for the Graph role lookup.
	// Insecure accepts its self-signed certificate (dev only).
	OpenCloudURL string
	Insecure     bool
	// Starting points for the administrator's choices.
	DefaultAnswerLanguage string
	UseUserLanguage       bool
	AllowMemories         bool
}

// instanceCredential is the platform instance this OpenCloud is
// registered as. It is replaced as one value, because background
// registration writes it while request handlers read it.
type instanceCredential struct {
	id     string
	secret string
}

type Handler struct {
	synaplanURL string
	synaplanAPI *synaplanapi.ClientWithResponses
	editor      synaplanapi.RequestEditorFn
	httpClient  *http.Client
	cs3         *cs3reader.Reader
	assetProxy  *httputil.ReverseProxy
	settings    Settings
	credential  atomic.Pointer[instanceCredential]
	state       *state.Store
	seen        *notify.Seen
	seenMu      sync.Mutex
	keysMu      sync.Mutex
	keys        []string
	keysLoaded  bool
	keysAt      time.Time
	indexMu     sync.Mutex
	watchIndex  []indexWatch
	indexAt     time.Time
	memoryCache
}

// New creates a new Handler. The editor is the synaplanapi
// RequestEditorFn that authenticates every outbound call to Synaplan
// — the caller picks between per-user OIDC token exchange and a
// shared API key (see pkg/command/server.go).
func New(editor synaplanapi.RequestEditorFn, synaplanURL string, cs3 *cs3reader.Reader, settings Settings) (*Handler, error) {
	apiClient, err := synaplanapi.NewClientWithResponses(
		synaplanURL,
		synaplanapi.WithHTTPClient(&http.Client{}),
		synaplanapi.WithRequestEditorFn(editor),
	)
	if err != nil {
		return nil, fmt.Errorf("creating synaplan API client: %w", err)
	}

	target, err := url.Parse(synaplanURL)
	if err != nil {
		return nil, fmt.Errorf("parsing synaplan url: %w", err)
	}
	assetProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Preserve any path prefix in synaplanURL (subpath deployments)
			// instead of clobbering it with just /name.
			pr.Out.URL.Path = "/" + strings.TrimLeft(path.Join(target.Path, chi.URLParam(pr.In, "name")), "/")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
		},
	}

	store, err := state.Open(settings.StatePath, settings.StateKey)
	if err != nil {
		return nil, fmt.Errorf("opening state: %w", err)
	}
	if cs3 != nil {
		cs3.SetMachineAuth(settings.MachineAuthKey)
	}
	h := &Handler{
		synaplanURL: synaplanURL,
		synaplanAPI: apiClient,
		editor:      editor,
		httpClient:  &http.Client{},
		cs3:         cs3,
		assetProxy:  assetProxy,
		settings:    settings,
		state:       store,
		seen:        notify.NewSeen(),
	}
	if settings.InstanceID != "" && settings.InstanceSecret != "" {
		h.setCredential(settings.InstanceID, settings.InstanceSecret)
	} else if id, secret := store.Instance(); id != "" && secret != "" {
		h.setCredential(id, secret)
	}
	return h, nil
}

func (h *Handler) setCredential(id, secret string) {
	h.credential.Store(&instanceCredential{id: id, secret: secret})
}

// instance returns the registered credential, or empty strings.
func (h *Handler) instance() (string, string) {
	if h == nil {
		return "", ""
	}
	cred := h.credential.Load()
	if cred == nil {
		return "", ""
	}
	return cred.id, cred.secret
}

// store is the process state. A handler built in a test has none, so
// this returns an in-memory store instead of nil.
func (h *Handler) store() *state.Store {
	if h != nil && h.state != nil {
		return h.state
	}
	opened, err := state.Open("", "")
	if err != nil {
		return nil
	}
	return opened
}

// writeJSON serialises v as JSON to w with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
