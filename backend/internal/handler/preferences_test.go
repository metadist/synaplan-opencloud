package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSavePreferencesRejectsABadRequest(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"not json", "{not json", "invalid JSON body"},
		{"unknown mode", `{"mode":"everything"}`, "Choose what Synaplan may read."},
		{"copy folder escapes", `{"mode":"watched","copyFolder":"../secrets"}`, "That folder cannot be used."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/synaplan/preferences", strings.NewReader(tc.body))
			(&Handler{}).SavePreferences(rr, req)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestSavePreferencesKeepsALocalSettingWhenNothingIsRegistered(t *testing.T) {
	// No instance credential: the setting is stored here and the
	// answer says Synaplan was not told. A bare Handler has that
	// in-memory store and no instance.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/synaplan/preferences", strings.NewReader(`{"mode":"watched","folders":[]}`))
	(&Handler{}).SavePreferences(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Saved    bool `json:"saved"`
		Reported bool `json:"reported"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Saved || got.Reported {
		t.Fatalf("got %+v, want saved locally and not reported", got)
	}
}
