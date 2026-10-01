package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// synaplanCall sends an authenticated JSON request to a Synaplan path
// that the generated client does not cover (check-stale, file delete).
// The same request editor used by the generated client attaches either
// the exchanged OIDC bearer or the shared API key.
func (h *Handler) synaplanCall(ctx context.Context, method, rel string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	endpoint := strings.TrimRight(h.synaplanURL, "/") + rel
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if h.editor != nil {
		if err := h.editor(ctx, req); err != nil {
			return 0, nil, err
		}
	}
	client := h.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read synaplan response: %w", err)
	}
	return resp.StatusCode, raw, nil
}
