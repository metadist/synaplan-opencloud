package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/preferences"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

// maxReadFolders bounds the folders one person can name, so a save stays
// one request to Synaplan and one stat per folder.
const maxReadFolders = 100

// saveAttempts bounds how often a save reports again after another
// replica stored a newer record. Two overlapping saves settle in a few
// attempts; past this the answer is that the save was not confirmed.
const saveAttempts = 8

// saveUnconfirmed is the sentence when this service cannot promise what
// Synaplan is enforcing. A lost answer can arrive after the scope was
// applied, so the previous scope is not a guarantee.
const saveUnconfirmed = "Synaplan did not confirm this save. Save again."

type preferencesAnswer struct {
	Available bool                   `json:"available"`
	ReadScope *preferences.ReadScope `json:"readScope,omitempty"`
}

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
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not check who you are. Try again.", Code: "identity_check_failed"})
		return
	}
	p, err := h.preferences.Get(r.Context(), sub)
	if err != nil {
		log.Printf("preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Your Synaplan settings cannot be read right now. Try again later.", Code: "settings_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, preferencesAnswer{Available: true, ReadScope: &p.ReadScope})
}

// SavePreferences tells Synaplan what the person lets it read, and keeps
// the setting here only once Synaplan took it: Synaplan enforces it, and
// this page must never show a setting Synaplan does not follow.
func (h *Handler) SavePreferences(w http.ResponseWriter, r *http.Request) {
	if h.preferences == nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Your own Synaplan settings need your own login. Ask your administrator.", Code: "settings_need_person"})
		return
	}
	var req preferences.ReadScope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	if !preferences.ValidMode(req.Mode) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Choose what Synaplan may read."})
		return
	}
	if len(req.Folders) > maxReadFolders {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Name at most 100 folders."})
		return
	}
	sub, err := h.signedInSubject(r.Context())
	if err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Synaplan could not check who you are. Try again.", Code: "identity_check_failed"})
		return
	}
	folders, err := h.resolveFolders(r, req.Folders)
	if err != nil {
		log.Printf("save preferences: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "OpenCloud could not check this folder. Try again.", Code: "folder_check_failed"})
		return
	}
	scope := preferences.ReadScope{Mode: req.Mode, Folders: folders}
	if failure := h.commitReadScope(r, sub, scope); failure != nil {
		writeJSON(w, http.StatusBadGateway, failure)
		return
	}
	writeJSON(w, http.StatusOK, preferencesAnswer{Available: true, ReadScope: &scope})
}

// commitReadScope reports scope to Synaplan and stores it only when the
// person's record is still the generation this attempt read. This process
// holds one lock per person across the report and the write. Another
// replica is outside that lock. It can store only if the revision still
// matches; if it does not, this attempt reports the same scope again.
//
// Comparing the revision cannot cover the Synaplan call itself. Two
// replicas can both report before either write lands. The write that
// loses reports again, so a pair of attempts that both finish stores the
// last scope that was reported. A replica that stops after Synaplan
// accepted the scope and before its write lands can leave Synaplan
// enforcing a scope this store does not hold. There is no lease in this
// bucket that would hold the other replica off for that call.
func (h *Handler) commitReadScope(r *http.Request, sub string, scope preferences.ReadScope) *errorResponse {
	unlock := h.lockSubject(sub)
	defer unlock()
	reported := false
	for range saveAttempts {
		match, err := h.preferences.Revision(r.Context(), sub)
		if err != nil {
			log.Printf("save preferences: %v", err)
			if reported {
				return &errorResponse{Error: saveUnconfirmed, Code: "not_reported"}
			}
			return &errorResponse{Error: "Your Synaplan settings cannot be read right now. Try again later.", Code: "settings_unavailable"}
		}
		if failure := h.reportReadScope(r, sub, scope); failure != nil {
			return failure
		}
		reported = true
		ok, err := h.preferences.Update(r.Context(), sub, preferences.Preferences{ReadScope: scope}, match)
		if err != nil {
			log.Printf("save preferences: %v", err)
			return &errorResponse{Error: "Synaplan follows the new setting, but this page could not keep it. Save again.", Code: "settings_not_kept"}
		}
		if ok {
			return nil
		}
	}
	log.Printf("save preferences: the record kept changing")
	return &errorResponse{Error: saveUnconfirmed, Code: "not_reported"}
}

// resolveFolders replaces whatever path the browser sent with the path
// OpenCloud knows for that id, as the person sees it, so a folder named
// like an allowed one, a display name, or a folder of someone else never
// widens what Synaplan may read. A folder that is gone, or not theirs to
// see, is dropped: that only narrows the setting, and a person is never
// stuck with a folder they cannot remove. A folder named twice counts once.
func (h *Handler) resolveFolders(r *http.Request, folders []preferences.Folder) ([]preferences.Folder, error) {
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
		path, err := h.folders.FolderPath(r.Context(), folder.ID)
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

// reportReadScope hands the setting to Synaplan as the signed-in person;
// Synaplan finds this cloud from the token's client and refuses anyone
// else's external id. It answers the failure to show, or nil.
func (h *Handler) reportReadScope(r *http.Request, sub string, scope preferences.ReadScope) *errorResponse {
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
	res, err := h.synaplanAPI.ReportCloudReadScopeWithResponse(r.Context(), body)
	if err != nil {
		log.Printf("report read scope: %v", err)
		return &errorResponse{Error: saveUnconfirmed, Code: "not_reported"}
	}
	switch res.StatusCode() {
	case http.StatusOK:
		return nil
	case http.StatusForbidden, http.StatusNotFound:
		log.Printf("report read scope: synaplan answered %d: %s", res.StatusCode(), res.Body)
		return &errorResponse{Error: "Synaplan does not accept settings from this OpenCloud yet. Ask your administrator.", Code: "cloud_not_linked"}
	case http.StatusConflict:
		log.Printf("report read scope: synaplan answered %d: %s", res.StatusCode(), res.Body)
		return &errorResponse{Error: "Synaplan knows this OpenCloud more than once. Ask your administrator.", Code: "cloud_ambiguous"}
	default:
		log.Printf("report read scope: synaplan answered %d", res.StatusCode())
		return &errorResponse{Error: saveUnconfirmed, Code: "not_reported"}
	}
}
