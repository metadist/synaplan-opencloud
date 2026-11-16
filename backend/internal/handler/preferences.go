package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/preferences"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// maxReadFolders bounds the folders one person can name, so a save stays
// one request to Synaplan and one stat per folder.
const maxReadFolders = 100

type preferencesAnswer struct {
	Available bool                   `json:"available"`
	ReadScope *preferences.ReadScope `json:"readScope,omitempty"`
}

// The answers the settings page turns into a sentence (failures.ts).
var (
	identityCheckFailed = errorResponse{Error: "Synaplan could not check who you are. Try again.", Code: "identity_check_failed"}
	settingsUnavailable = errorResponse{Error: "Your Synaplan settings are not available right now. Try again later.", Code: "settings_unavailable"}
	notConfirmed        = errorResponse{Error: "Synaplan did not confirm this setting. Save again to be sure it applies.", Code: "not_confirmed"}
)

// Preferences is what the signed-in person lets Synaplan read. With the
// shared API key there is no person to keep settings for, so the answer
// is that there are none.
func (h *Handler) Preferences(w http.ResponseWriter, r *http.Request) {
	if h.preferences == nil {
		writeJSON(w, http.StatusOK, preferencesAnswer{})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, identityCheckFailed)
		return
	}
	p, err := h.preferences.Get(r.Context(), sub)
	if err != nil {
		log.Printf("preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, settingsUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, preferencesAnswer{Available: true, ReadScope: &p.ReadScope})
}

// SavePreferences tells Synaplan what the person lets it read, and keeps
// the setting here only once Synaplan took it: Synaplan enforces it, and
// this page must never show a setting Synaplan does not follow. Reporting
// and keeping run under the person's lease, so two saves at once (two
// tabs, two replicas) cannot leave this copy behind what Synaplan holds.
func (h *Handler) SavePreferences(w http.ResponseWriter, r *http.Request) {
	var req preferences.ReadScope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "The setting could not be read. Reload the page and try again.", Code: "invalid_setting"})
		return
	}
	if !preferences.ValidMode(req.Mode) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose what Synaplan may read.", Code: "invalid_setting"})
		return
	}
	if len(req.Folders) > maxReadFolders {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("Allow at most %d folders.", maxReadFolders), Code: "too_many_folders"})
		return
	}
	if h.preferences == nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Your own Synaplan settings need your own login. Ask your administrator.", Code: "settings_need_person"})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, identityCheckFailed)
		return
	}
	held, release, err := h.preferences.Lease(r.Context(), sub)
	if err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, settingsUnavailable)
		return
	}
	defer release()
	folders, err := h.resolveFolders(held, req.Folders)
	if errors.Is(err, cs3reader.ErrNotPersonal) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Only folders in your personal space can be allowed.", Code: "folder_not_personal"})
		return
	}
	if err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "OpenCloud could not check this folder. Try again.", Code: "folder_check_failed"})
		return
	}
	scope := preferences.ReadScope{Mode: req.Mode, Folders: folders}
	if err := h.reportPreferenceReadScope(held, sub, scope); err != nil {
		log.Printf("save preferences: %v", err)
		answer := notConfirmed
		var refused *readScopeRefused
		if errors.As(err, &refused) {
			answer = refused.answer
		}
		writeJSON(w, http.StatusBadGateway, answer)
		return
	}
	if err := h.preferences.Put(held, sub, preferences.Preferences{ReadScope: scope}); err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan follows the new setting, but this page could not keep it. Save again.", Code: "settings_not_kept"})
		return
	}
	writeJSON(w, http.StatusOK, preferencesAnswer{Available: true, ReadScope: &scope})
}

// resolveFolders replaces whatever path the browser sent with the path
// OpenCloud knows for that id in the person's personal space, so a folder
// named like an allowed one, a display name, or a folder of a share or a
// project space never widens what Synaplan may read. A folder that is
// gone, or not theirs to see, is dropped: that only narrows the setting,
// and a person is never stuck with a folder they cannot remove. A folder
// outside the personal space refuses the save (cs3reader.ErrNotPersonal).
// A folder named twice counts once.
func (h *Handler) resolveFolders(ctx context.Context, folders []preferences.Folder) ([]preferences.Folder, error) {
	out := make([]preferences.Folder, 0, len(folders))
	seen := map[string]bool{}
	for _, folder := range folders {
		if folder.ID == "" || seen[folder.ID] {
			continue
		}
		seen[folder.ID] = true
		if h.folders == nil {
			return nil, errors.New("resolve folder: storage is not configured")
		}
		path, err := h.folders.FolderPath(ctx, folder.ID)
		if errors.Is(err, cs3reader.ErrNoFolder) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, preferences.Folder{ID: folder.ID, Path: path})
	}
	return out, nil
}

// readScopeRefused is Synaplan saying no for a reason the person or their
// administrator can act on; nothing changed in Synaplan.
type readScopeRefused struct {
	status int
	answer errorResponse
}

func (e *readScopeRefused) Error() string {
	return fmt.Sprintf("report read scope: synaplan answered %d", e.status)
}

// reportPreferenceReadScope hands the person's own setting to Synaplan.
// Synaplan finds this cloud from the token's client and refuses anyone
// else's external id. A refusal is a *readScopeRefused; any other error
// leaves open whether Synaplan applied the setting (a lost answer).
func (h *Handler) reportPreferenceReadScope(ctx context.Context, sub string, scope preferences.ReadScope) error {
	body := synaplanapi.ReportCloudReadScopeJSONRequestBody{
		ExternalId: sub,
		Mode:       synaplanapi.ReportCloudReadScopeJSONBodyMode(scope.Mode),
		Folders: make([]struct {
			Id   *string `json:"id,omitempty"`
			Path *string `json:"path,omitempty"`
		}, len(scope.Folders)),
	}
	for i := range scope.Folders {
		body.Folders[i].Id = &scope.Folders[i].ID
		body.Folders[i].Path = &scope.Folders[i].Path
	}
	res, err := h.synaplanAPI.ReportCloudReadScopeWithResponse(ctx, body)
	if err != nil {
		return fmt.Errorf("report read scope: %w", err)
	}
	switch res.StatusCode() {
	case http.StatusOK:
		return nil
	case http.StatusForbidden, http.StatusNotFound:
		log.Printf("report read scope: synaplan answered %d: %s", res.StatusCode(), res.Body)
		return &readScopeRefused{status: res.StatusCode(), answer: errorResponse{Error: "Synaplan does not accept settings from this OpenCloud yet. Ask your administrator.", Code: "cloud_not_linked"}}
	case http.StatusConflict:
		log.Printf("report read scope: synaplan answered %d: %s", res.StatusCode(), res.Body)
		return &readScopeRefused{status: res.StatusCode(), answer: errorResponse{Error: "Synaplan knows this OpenCloud more than once. Ask your administrator.", Code: "cloud_ambiguous"}}
	default:
		return fmt.Errorf("report read scope: synaplan answered %d", res.StatusCode())
	}
}
