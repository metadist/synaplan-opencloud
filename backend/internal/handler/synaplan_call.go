package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

type onBehalfOfKey struct{}

type anonymousKey struct{}

// anonymous marks ctx for a call to one of Synaplan's open documents (its
// configuration and key set), which carries no credential.
func anonymous(ctx context.Context) context.Context {
	return context.WithValue(ctx, anonymousKey{}, true)
}

// onBehalfOf marks ctx for a call made for sub while they are away: the
// generated client then carries a Synaplan token minted from their offline
// token instead of the signed-in person's exchanged token.
func onBehalfOf(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, onBehalfOfKey{}, sub)
}

// authenticate is the generated client's credential editor. A call made
// for a person who is away carries their own Synaplan token, a call for
// an open document none; any other call carries what the editor picked at
// startup: the signed-in person's exchanged token, or the shared API key.
func (h *Handler) authenticate(ctx context.Context, req *http.Request) error {
	if open, _ := ctx.Value(anonymousKey{}).(bool); open {
		return nil
	}
	if sub, _ := ctx.Value(onBehalfOfKey{}).(string); sub != "" {
		token, err := h.delegatedBearer(ctx, sub)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	if h.editor == nil {
		return nil
	}
	return h.editor(ctx, req)
}

// errOpenCloudTokenForwarded is the one mistake this service must never
// make: the token OpenCloud's proxy hands us was issued for OpenCloud.
// Synaplan gets the token the exchange minted for it, a person's own
// Synaplan token, or the shared API key, never that one.
var errOpenCloudTokenForwarded = errors.New("refusing to send the OpenCloud token to Synaplan; exchange it first")

// refuseOpenCloudToken is the generated client's last editor. Every
// credential editor already exchanges; this makes a future editor or
// copy-paste that forwards the incoming token fail loudly instead of
// quietly working against a Synaplan that happens to accept it.
func refuseOpenCloudToken(ctx context.Context, req *http.Request) error {
	incoming := synaplanauth.OIDCTokenFromContext(ctx)
	if incoming == "" {
		return nil
	}
	outgoing := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if outgoing != "" && outgoing == incoming {
		return errOpenCloudTokenForwarded
	}
	return nil
}

// withBody gives a generated call the request body its operation does not
// declare. Synaplan's cloud watch routes read a JSON or multipart body but
// publish none in the OpenAPI spec, so their generated calls send none;
// this keeps them on the generated client and its credential editors.
func withBody(contentType string, body io.Reader) synaplanapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Content-Type", contentType)
		if b, ok := body.(*bytes.Reader); ok {
			req.ContentLength = int64(b.Len())
		}
		req.Body = io.NopCloser(body)
		return nil
	}
}

// withJSON is withBody for a JSON value.
func withJSON(v any) (synaplanapi.RequestEditorFn, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding request body: %w", err)
	}
	return withBody("application/json", bytes.NewReader(raw)), nil
}
