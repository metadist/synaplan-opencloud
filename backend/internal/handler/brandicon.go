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
	"strings"
	"time"
)

const (
	brandIconMaxBytes    = 1 << 20
	brandIconTimeout     = 10 * time.Second
	brandIconCacheMaxAge = 300
)

// BrandIcon replays Synaplan's brand icon from this origin because
// OpenCloud's CSP only allows img-src 'self'. Synaplan decides which
// image that is; there is deliberately no fallback here, so a
// white-label install never shows another brand's mark.
func (h *Handler) BrandIcon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), brandIconTimeout)
	defer cancel()

	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}

	contentType, body, err := h.fetchBrandIcon(ctx, theme)
	if err != nil {
		log.Printf("brand icon: %v", err)
		http.Error(w, "brand icon unavailable", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", brandIconCacheMaxAge))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An SVG opened directly would otherwise run its scripts on
	// the OpenCloud origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	_, _ = w.Write(body)
}

func (h *Handler) fetchBrandIcon(ctx context.Context, theme string) (string, []byte, error) {
	iconURL, err := h.brandIconURL(ctx, theme)
	if err != nil {
		return "", nil, fmt.Errorf("runtime config: %w", err)
	}

	resp, err := h.getFromSynaplan(ctx, iconURL)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return "", nil, fmt.Errorf("not an image: %q", contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, brandIconMaxBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(body) > brandIconMaxBytes {
		return "", nil, errors.New("image larger than 1 MiB")
	}
	return contentType, body, nil
}

// brandIconURL reads the icon Synaplan resolved for the theme
// from its public runtime config.
func (h *Handler) brandIconURL(ctx context.Context, theme string) (string, error) {
	resp, err := h.getFromSynaplan(ctx, "api/v1/config/runtime")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var cfg struct {
		Branding struct {
			ResolvedIconURL     string `json:"resolvedIconUrl"`
			ResolvedIconDarkURL string `json:"resolvedIconDarkUrl"`
		} `json:"branding"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, brandIconMaxBytes)).Decode(&cfg); err != nil {
		return "", err
	}

	iconURL := cfg.Branding.ResolvedIconURL
	if theme == "dark" {
		iconURL = cfg.Branding.ResolvedIconDarkURL
	}
	if iconURL == "" {
		return "", errors.New("no resolved icon in the runtime config, Synaplan too old?")
	}
	return iconURL, nil
}

// getFromSynaplan requests ref, resolved against the Synaplan base URL
// so relative icon URLs and subpath deployments work.
func (h *Handler) getFromSynaplan(ctx context.Context, ref string) (*http.Response, error) {
	base, err := url.Parse(strings.TrimRight(h.synaplanURL, "/") + "/")
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		parsed.Path = strings.TrimLeft(parsed.Path, "/")
	}
	target := base.ResolveReference(parsed)
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", target.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: status %d", target.Path, resp.StatusCode)
	}
	return resp, nil
}
