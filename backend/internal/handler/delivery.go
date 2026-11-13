package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/notify"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

const (
	// maxDeliveryBytes is the largest copy this service takes from
	// Synaplan; the copy is held in memory until it is written.
	maxDeliveryBytes = 100 << 20
	// deliveryTimeout bounds claiming, downloading, writing and reporting
	// one copy. Synaplan waits 30 seconds for the answer to its
	// notification; a copy that takes longer is still written and then
	// reported on the result route.
	deliveryTimeout = 2 * time.Minute
	// targetKindFolder is Synaplan's target for a copy without a folder:
	// the person's Synaplan folder for the file's kind.
	targetKindFolder = "kind_folder"
)

// Delivery outcomes and the failure codes Synaplan knows
// (DestinationFailureCode).
const (
	deliveryDelivered = "delivered"
	deliveryFailed    = "failed"
	failUnauthorized  = "unauthorized"
	failTooLarge      = "too_large"
	failUnreachable   = "unreachable"
	failUnsupported   = "unsupported"
)

// errClaimLater means the copy was not claimed and Synaplan's retry of the
// same notification should be let in; errClaimRefused means Synaplan
// refused the claim (taken, expired, unknown), which no retry changes.
var (
	errClaimLater   = errors.New("delivery not claimed, try again")
	errClaimRefused = errors.New("delivery claim refused")
)

// deliveryFiles is where a copy is written. The reva token on the context
// decides as whom: machine auth for the person the copy is for.
type deliveryFiles interface {
	Save(ctx context.Context, filename string, content []byte) (string, error)
}

// deliveryResult is what Synaplan records for a copy, both as the answer
// to its notification and on the result route.
type deliveryResult struct {
	Status     string `json:"status"`
	RemotePath string `json:"remote_path,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// deliveryClaim is Synaplan's answer to a claim: whose copy it is, its
// name and size, where it goes, and the one-time path to download it.
type deliveryClaim struct {
	ExternalID   string `json:"external_id"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	DownloadPath string `json:"download_path"`
	Target       struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	} `json:"target"`
}

// deliverCopy handles a verified delivery notification: Synaplan has a
// file for the person it names. Synaplan only notifies; this service
// claims and downloads the copy with that person's own Synaplan token and
// writes it into their OpenCloud with a reva token from machine auth, so
// it works while they are away. A copy that could not be claimed lets
// Synaplan's retry of the same notification in.
func (h *Handler) deliverCopy(w http.ResponseWriter, r *http.Request, claims notify.Claims) {
	id, err := strconv.Atoi(claims.ID)
	if err != nil || id <= 0 || claims.Subject == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "bad_notification"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), deliveryTimeout)
	defer cancel()
	away, err := h.actAs(ctx, claims.Subject)
	if err != nil {
		h.forgetHandled(claims)
		log.Printf("delivery %d: acting for the person: %v", id, err)
		if errors.Is(err, delegation.ErrNotConnected) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "not_connected"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "unavailable"})
		return
	}
	result, err := h.deliver(away, h.cs3, claims.Subject, id)
	if err != nil {
		log.Printf("delivery %d: %v", id, err)
		if errors.Is(err, errClaimLater) {
			h.forgetHandled(claims)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// deliver claims copy id for sub, writes it with files and reports the
// outcome to Synaplan. ctx carries sub's credentials.
func (h *Handler) deliver(ctx context.Context, files deliveryFiles, sub string, id int) (deliveryResult, error) {
	claim, err := h.claimDelivery(ctx, id)
	if err != nil {
		return deliveryResult{}, err
	}
	result := h.writeDelivery(ctx, files, sub, id, claim)
	h.reportDelivery(ctx, id, result)
	return result, nil
}

func (h *Handler) claimDelivery(ctx context.Context, id int) (deliveryClaim, error) {
	resp, err := h.synaplanAPI.ClaimCloudDeliveryWithResponse(ctx, id)
	if err != nil {
		return deliveryClaim{}, fmt.Errorf("%w: %v", errClaimLater, err)
	}
	switch status := resp.StatusCode(); {
	case status == http.StatusOK:
	case status >= http.StatusInternalServerError:
		return deliveryClaim{}, fmt.Errorf("%w: synaplan answered %d", errClaimLater, status)
	default:
		return deliveryClaim{}, fmt.Errorf("%w: %v", errClaimRefused, upstream(status, resp.Body))
	}
	var claim deliveryClaim
	if err := json.Unmarshal(resp.Body, &claim); err != nil {
		return deliveryClaim{}, fmt.Errorf("%w: decoding the claim: %v", errClaimRefused, err)
	}
	return claim, nil
}

// writeDelivery downloads a claimed copy and writes it. A claim that names
// another person than the one the notification named is not written: the
// copy would land in the wrong person's files.
func (h *Handler) writeDelivery(ctx context.Context, files deliveryFiles, sub string, id int, claim deliveryClaim) deliveryResult {
	if claim.ExternalID != sub {
		log.Printf("delivery %d: the claim names another person", id)
		return deliveryResult{Status: deliveryFailed, ErrorCode: failUnauthorized}
	}
	ticket, ok := downloadTicket(id, claim.DownloadPath)
	if !ok {
		log.Printf("delivery %d: unexpected download path", id)
		return deliveryResult{Status: deliveryFailed, ErrorCode: failUnreachable}
	}
	if claim.Target.Kind != "" && claim.Target.Kind != targetKindFolder {
		log.Printf("delivery %d: target %q is not supported", id, claim.Target.Kind)
		return deliveryResult{Status: deliveryFailed, ErrorCode: failUnsupported}
	}
	if claim.Size > maxDeliveryBytes {
		return deliveryResult{Status: deliveryFailed, ErrorCode: failTooLarge}
	}
	content, err := h.downloadDelivery(ctx, id, ticket)
	if errors.Is(err, errTooLarge) {
		return deliveryResult{Status: deliveryFailed, ErrorCode: failTooLarge}
	}
	if err != nil {
		log.Printf("delivery %d: download: %v", id, err)
		return deliveryResult{Status: deliveryFailed, ErrorCode: failUnreachable}
	}
	name := claim.Name
	if name == "" {
		name = "file"
	}
	written, err := files.Save(ctx, name, content)
	if err != nil {
		log.Printf("delivery %d: write: %v", id, err)
		return deliveryResult{Status: deliveryFailed, ErrorCode: failUnreachable}
	}
	return deliveryResult{Status: deliveryDelivered, RemotePath: "/" + written}
}

// errTooLarge means a copy is larger than maxDeliveryBytes.
var errTooLarge = errors.New("copy is too large")

func (h *Handler) downloadDelivery(ctx context.Context, id int, ticket string) ([]byte, error) {
	resp, err := h.synaplanAPI.DownloadCloudDelivery(ctx, id, &synaplanapi.DownloadCloudDeliveryParams{Ticket: ticket})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("synaplan answered %d", resp.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxDeliveryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxDeliveryBytes {
		return nil, errTooLarge
	}
	return content, nil
}

// downloadTicket is the one-time ticket of the download path a claim
// names. Only the path of this copy is accepted: the download goes through
// the generated client, never to a URL from an answer.
func downloadTicket(id int, downloadPath string) (string, bool) {
	parsed, err := url.Parse(downloadPath)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "", false
	}
	if parsed.Path != "/api/v1/platform-links/deliveries/"+strconv.Itoa(id)+"/file" {
		return "", false
	}
	ticket := parsed.Query().Get("ticket")
	return ticket, ticket != ""
}

// reportDelivery records the outcome on Synaplan's result route, so it is
// kept even when Synaplan stopped waiting for the notification's answer.
func (h *Handler) reportDelivery(ctx context.Context, id int, result deliveryResult) {
	payload, err := withJSON(result)
	if err != nil {
		log.Printf("delivery %d: report: %v", id, err)
		return
	}
	resp, err := h.synaplanAPI.RecordCloudDeliveryResultWithResponse(ctx, id, payload)
	if err != nil {
		log.Printf("delivery %d: report: %v", id, err)
		return
	}
	if resp.StatusCode() != http.StatusOK {
		log.Printf("delivery %d: report: %v", id, upstream(resp.StatusCode(), resp.Body))
	}
}
