package tokenexchange

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExchangeAcceptsCertificateWhenVerificationIsSkipped(t *testing.T) {
	srv := tokenServer(t)
	defer srv.Close()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the case under test
	token, err := exchangeAgainst(srv.URL, &http.Client{Timeout: 5 * time.Second, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if token != "exchanged" {
		t.Fatalf("token %q", token)
	}
}

func TestExchangeRejectsUnknownCertificate(t *testing.T) {
	srv := tokenServer(t)
	defer srv.Close()

	_, err := exchangeAgainst(srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err %v, want a certificate failure", err)
	}
}

func tokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"exchanged","expires_in":60,"token_type":"Bearer"}`)
	}))
}

func exchangeAgainst(endpoint string, client *http.Client) (string, error) {
	ex := New(func(context.Context) (string, error) { return endpoint, nil }, "id", "secret", "aud", client)
	return ex.Exchange("ada", "subject")
}
