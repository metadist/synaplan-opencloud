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

// callOptions tunes one outbound Synaplan request.
type callOptions struct {
	// extra headers are set after the user editor, so an instance
	// credential or a background key wins over the browser token.
	extra http.Header
	// skipUser leaves off the exchanged bearer. Instance operations
	// (claim a copy, report a read) authenticate with the instance secret.
	skipUser bool
	// apiKey sends X-API-Key instead of the browser token. Used for the
	// key that works while the person is away, and for registration.
	apiKey string
	// raw is a body that is already encoded (multipart). payload is ignored.
	raw         io.Reader
	contentType string
	limit       int64
}

// synaplanCall sends an authenticated JSON request to a Synaplan path
// that the generated client does not cover (check-stale, file delete,
// and the cloud-file routes). The same request editor used by the
// generated client attaches either the exchanged OIDC bearer or the
// shared API key.
func (h *Handler) synaplanCall(ctx context.Context, method, rel string, payload any) (int, []byte, error) {
	return h.synaplanDo(ctx, method, rel, payload, callOptions{})
}

func (h *Handler) synaplanDo(ctx context.Context, method, rel string, payload any, opt callOptions) (int, []byte, error) {
	var body io.Reader
	if opt.raw != nil {
		body = opt.raw
	} else if payload != nil {
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
	if opt.contentType != "" {
		req.Header.Set("Content-Type", opt.contentType)
	} else if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if !opt.skipUser && opt.apiKey == "" && h.editor != nil {
		if err := h.editor(ctx, req); err != nil {
			return 0, nil, err
		}
	}
	if opt.apiKey != "" {
		req.Header.Set("X-API-Key", opt.apiKey)
	}
	for key, values := range opt.extra {
		for _, value := range values {
			req.Header.Set(key, value)
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
	limit := opt.limit
	if limit <= 0 {
		limit = 8 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read synaplan response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

func (h *Handler) instanceHeaders() http.Header {
	return http.Header{
		"X-Instance-Id":     {h.instanceID},
		"X-Instance-Secret": {h.instanceSecret},
	}
}
