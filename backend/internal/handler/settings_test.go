package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/settings"
)

type fakePermissions struct {
	admin bool
	err   error
	asked []string
}

func (p *fakePermissions) Has(_ context.Context, permission string) (bool, error) {
	p.asked = append(p.asked, permission)
	return p.admin, p.err
}

type failingSettings struct{}

func (failingSettings) Get(context.Context) (settings.Values, bool, error) {
	return settings.Values{}, false, errors.New("nats down")
}
func (failingSettings) Put(context.Context, settings.Values) error { return errors.New("nats down") }
func (failingSettings) Close() error                               { return nil }

func storeWith(t *testing.T, values *settings.Values) *settings.MemoryStore {
	t.Helper()
	s := settings.NewMemoryStore()
	if values != nil {
		if err := s.Put(context.Background(), *values); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestNormalizeLanguage(t *testing.T) {
	tests := map[string]string{
		"de": "de", "DE": "de", "pt_BR": "pt", " fr-CA ": "fr", "xx": "", "": "", "d": "",
	}
	for raw, want := range tests {
		if got := normalizeLanguage(raw); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestResolveLanguage(t *testing.T) {
	tests := []struct {
		name     string
		defaults settings.Values
		store    settings.Store
		ui       string
		want     string
	}{
		{name: "no configuration", ui: "de", want: "en"},
		{name: "configured default", defaults: settings.Values{DefaultAnswerLanguage: "fr"}, ui: "de", want: "fr"},
		{name: "configured default unsupported", defaults: settings.Values{DefaultAnswerLanguage: "xx"}, want: "en"},
		{name: "the person's language", defaults: settings.Values{DefaultAnswerLanguage: "fr", UseUserLanguage: true}, ui: "de_DE", want: "de"},
		{name: "the person's language unsupported", defaults: settings.Values{DefaultAnswerLanguage: "fr", UseUserLanguage: true}, ui: "sv", want: "fr"},
		{name: "saved over configured", defaults: settings.Values{DefaultAnswerLanguage: "fr", UseUserLanguage: true}, store: storeWith(t, &settings.Values{DefaultAnswerLanguage: "it"}), ui: "de", want: "it"},
		{name: "nothing saved", defaults: settings.Values{DefaultAnswerLanguage: "fr"}, store: storeWith(t, nil), want: "fr"},
		{name: "store down", defaults: settings.Values{DefaultAnswerLanguage: "fr", UseUserLanguage: true}, store: failingSettings{}, ui: "es", want: "es"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{settings: tt.store, settingsDefaults: tt.defaults}
			if got := h.resolveLanguage(context.Background(), tt.ui); got != tt.want {
				t.Fatalf("resolveLanguage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSettings(t *testing.T) {
	tests := []struct {
		name        string
		permissions PermissionChecker
		sharedKey   bool
		store       settings.Store
		want        settingsResponse
	}{
		{
			name:        "a person in organisation mode",
			permissions: &fakePermissions{},
			store:       storeWith(t, &settings.Values{DefaultAnswerLanguage: "de", UseUserLanguage: true}),
			want:        settingsResponse{Mode: "organisation", DefaultAnswerLanguage: "de", UseUserLanguage: true},
		},
		{
			name:        "an administrator",
			permissions: &fakePermissions{admin: true},
			store:       storeWith(t, nil),
			want:        settingsResponse{Mode: "organisation", IsAdmin: true, DefaultAnswerLanguage: "en"},
		},
		{
			name:        "OpenCloud cannot tell",
			permissions: &fakePermissions{admin: true, err: errors.New("gateway down")},
			sharedKey:   true,
			store:       failingSettings{},
			want:        settingsResponse{Mode: "shared", DefaultAnswerLanguage: "en"},
		},
		{
			name:      "no permission check",
			sharedKey: true,
			want:      settingsResponse{Mode: "shared", DefaultAnswerLanguage: "en"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{permissions: tt.permissions, settings: tt.store, sharedKey: tt.sharedKey}
			rr := httptest.NewRecorder()
			h.Settings(rr, httptest.NewRequest(http.MethodGet, "/api/synaplan/settings", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var got settingsResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Languages) != len(answerLanguages) || got.Languages[0].Code != "ar" {
				t.Fatalf("languages = %+v", got.Languages)
			}
			got.Languages = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSaveSettings(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		permissions *fakePermissions
		store       settings.Store
		status      int
		code        string
		saved       *settings.Values
	}{
		{name: "not json", body: "{", status: http.StatusBadRequest},
		{name: "no language", body: `{"useUserLanguage":true}`, status: http.StatusBadRequest, code: "unsupported_language"},
		{name: "unlisted language", body: `{"defaultAnswerLanguage":"sv"}`, status: http.StatusBadRequest, code: "unsupported_language"},
		{name: "a locale, not a listed code", body: `{"defaultAnswerLanguage":"de_DE"}`, status: http.StatusBadRequest, code: "unsupported_language"},
		{name: "not an administrator", body: `{"defaultAnswerLanguage":"de"}`, permissions: &fakePermissions{}, store: storeWith(t, nil), status: http.StatusForbidden, code: "admin_only"},
		{name: "OpenCloud cannot tell", body: `{"defaultAnswerLanguage":"de"}`, permissions: &fakePermissions{admin: true, err: errors.New("down")}, store: storeWith(t, nil), status: http.StatusForbidden, code: "admin_only"},
		{name: "store down", body: `{"defaultAnswerLanguage":"de"}`, permissions: &fakePermissions{admin: true}, store: failingSettings{}, status: http.StatusServiceUnavailable, code: "settings_unavailable"},
		{name: "no store", body: `{"defaultAnswerLanguage":"de"}`, permissions: &fakePermissions{admin: true}, status: http.StatusServiceUnavailable, code: "settings_unavailable"},
		{name: "saved", body: `{"defaultAnswerLanguage":"DE","useUserLanguage":true}`, permissions: &fakePermissions{admin: true}, store: storeWith(t, nil), status: http.StatusOK, saved: &settings.Values{DefaultAnswerLanguage: "de", UseUserLanguage: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{settings: tt.store}
			if tt.permissions != nil {
				h.permissions = tt.permissions
			}
			rr := httptest.NewRecorder()
			h.SaveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/synaplan/settings", strings.NewReader(tt.body)))
			if rr.Code != tt.status {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var got errorResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &got)
			if got.Code != tt.code {
				t.Fatalf("code = %q, want %q", got.Code, tt.code)
			}
			if tt.permissions != nil && (len(tt.permissions.asked) != 1 || tt.permissions.asked[0] != adminPermission) {
				t.Fatalf("asked for %v", tt.permissions.asked)
			}
			if tt.saved != nil {
				stored, ok, err := tt.store.Get(context.Background())
				if err != nil || !ok || stored != *tt.saved {
					t.Fatalf("stored %+v, %v, %v; want %+v", stored, ok, err, *tt.saved)
				}
			}
		})
	}
}
