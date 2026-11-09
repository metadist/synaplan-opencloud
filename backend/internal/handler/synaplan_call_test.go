package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

const openCloudToken = "eyJ.opencloud.token"

// The token OpenCloud's proxy hands this service was issued for OpenCloud.
// Whatever the credential editor does, it must never reach Synaplan.
func TestTheOpenCloudTokenNeverReachesSynaplan(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	defer upstream.Close()
	forwarding := func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+synaplanauth.OIDCTokenFromContext(ctx))
		return nil
	}
	h, err := New(forwarding, upstream.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.synaplanAPI.GetApiAuthMeWithResponse(synaplanauth.ContextWithOIDCToken(context.Background(), openCloudToken))
	if !errors.Is(err, errOpenCloudTokenForwarded) {
		t.Fatalf("err = %v, want errOpenCloudTokenForwarded", err)
	}
	if reached {
		t.Fatal("a request carrying the OpenCloud token reached Synaplan")
	}
}

func TestEachCallCarriesTheRightCredential(t *testing.T) {
	idp := fakeIdentityProvider(t, "ada")
	cases := map[string]struct {
		ctx  context.Context
		want string
	}{
		"the signed-in person":       {context.Background(), "Bearer exchanged"},
		"a person who is away":       {onBehalfOf(context.Background(), "ada"), "Bearer for-synaplan-app"},
		"a person who never allowed": {onBehalfOf(context.Background(), "eve"), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := ""
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
			}))
			defer upstream.Close()
			exchanged := func(_ context.Context, req *http.Request) error {
				req.Header.Set("Authorization", "Bearer exchanged")
				return nil
			}
			h, err := New(exchanged, upstream.URL, nil, delegated(t, idp, "ada"))
			if err != nil {
				t.Fatal(err)
			}

			_, err = h.synaplanAPI.GetApiAuthMeWithResponse(tc.ctx)

			if tc.want == "" {
				if err == nil || got != "" {
					t.Fatalf("err = %v, sent %q; want no request", err, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("err = %v, Authorization = %q, want %q", err, got, tc.want)
			}
		})
	}
}

func TestOpenDocumentsCarryNoCredential(t *testing.T) {
	got := "unset"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization") + r.Header.Get("X-API-Key")
	}))
	defer upstream.Close()
	h, err := New(func(_ context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", "sk_shared")
		return nil
	}, upstream.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.synaplanAPI.JwksWithResponse(anonymous(context.Background())); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("credential %q sent with an open document", got)
	}
}
