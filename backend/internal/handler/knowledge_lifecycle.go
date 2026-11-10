package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

type knowledgeLookupError struct {
	code int
	msg  string
}

func (e *knowledgeLookupError) Error() string { return e.msg }

type knowledgeStatusResponse struct {
	InKnowledge    bool `json:"inKnowledge"`
	Stale          bool `json:"stale"`
	SynaplanFileID int  `json:"synaplanFileId"`
}

// KnowledgeStatus reports whether this OpenCloud file is already in
// the signed-in user's Synaplan knowledge base, and whether the
// stored copy is older than the file they have now.
func (h *Handler) KnowledgeStatus(w http.ResponseWriter, r *http.Request) {
	resourceID := r.URL.Query().Get("resourceId")
	if resourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	resp, err := h.knowledgeStatusFor(r.Context(), resourceID)
	if err != nil {
		log.Printf("knowledge status: %v", err)
		writeJSON(w, statusCodeForKnowledge(err), errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// knowledgeStatusFor stats the OpenCloud file (the access check) and
// asks Synaplan which knowledge copy, if any, belongs to that file.
func (h *Handler) knowledgeStatusFor(ctx context.Context, resourceID string) (knowledgeStatusResponse, error) {
	if h.cs3 == nil {
		return knowledgeStatusResponse{}, &knowledgeLookupError{code: http.StatusBadGateway, msg: "could not read file: storage is not configured"}
	}
	file, err := h.cs3.Stat(ctx, resourceID)
	if err != nil {
		return knowledgeStatusResponse{}, &knowledgeLookupError{code: http.StatusBadGateway, msg: "could not read file: " + err.Error()}
	}

	source, etag := "opencloud", file.Etag
	body := synaplanapi.PostApiFilesCheckStaleJSONRequestBody{Source: &source}
	body.Items = append(body.Items, struct {
		SourceEtag *string `json:"source_etag,omitempty"`
		SourceId   *string `json:"source_id,omitempty"`
	}{SourceEtag: &etag, SourceId: &resourceID})
	checked, err := h.synaplanAPI.PostApiFilesCheckStaleWithResponse(ctx, body)
	if err != nil {
		return knowledgeStatusResponse{}, &knowledgeLookupError{code: http.StatusBadGateway, msg: "could not check knowledge status: " + err.Error()}
	}
	// Older Synaplan builds have no check-stale route. Treat that as
	// "not indexed yet" so Add still works.
	if status := checked.StatusCode(); status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return knowledgeStatusResponse{}, nil
	}
	if checked.JSON200 == nil {
		return knowledgeStatusResponse{}, &knowledgeLookupError{
			code: http.StatusBadGateway,
			msg:  fmt.Sprintf("knowledge status %d: %s", checked.StatusCode(), truncate(string(checked.Body), 300)),
		}
	}
	if checked.JSON200.Results == nil || len(*checked.JSON200.Results) == 0 {
		return knowledgeStatusResponse{}, nil
	}
	row := (*checked.JSON200.Results)[0]
	state := ""
	if row.Status != nil {
		state = string(*row.Status)
	}
	resp := knowledgeStatusResponse{
		InKnowledge: state != "" && state != "missing",
		Stale:       state == "stale",
	}
	if row.FileId != nil {
		resp.SynaplanFileID = *row.FileId
	}
	return resp, nil
}

func statusCodeForKnowledge(err error) int {
	var lookup *knowledgeLookupError
	if errors.As(err, &lookup) {
		return lookup.code
	}
	return http.StatusBadGateway
}

// RemoveFromKnowledge deletes the Synaplan knowledge copy of one
// OpenCloud file. The Synaplan id is resolved from that file after a
// CS3 access check, so a caller cannot name some other file by id.
func (h *Handler) RemoveFromKnowledge(w http.ResponseWriter, r *http.Request) {
	resourceID := r.URL.Query().Get("resourceId")
	if resourceID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "resourceId is required"})
		return
	}
	status, err := h.knowledgeStatusFor(r.Context(), resourceID)
	if err != nil {
		log.Printf("knowledge remove: %v", err)
		writeJSON(w, statusCodeForKnowledge(err), errorResponse{Error: err.Error()})
		return
	}
	if !status.InKnowledge || status.SynaplanFileID <= 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "this file is not in the knowledge base"})
		return
	}
	removed, err := h.synaplanAPI.DeleteApiFilesDeleteWithResponse(r.Context(), status.SynaplanFileID)
	if err != nil {
		log.Printf("knowledge remove: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not remove from knowledge: " + err.Error()})
		return
	}
	if code := removed.StatusCode(); code != http.StatusOK && code != http.StatusNoContent {
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error: fmt.Sprintf("remove status %d: %s", code, truncate(string(removed.Body), 300)),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}
