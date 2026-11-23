package command

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authpb "github.com/cs3org/go-cs3apis/cs3/auth/provider/v1beta1"
	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	"github.com/opencloud-eu/opencloud/pkg/account"
	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/pkg/middleware"
	revactx "github.com/opencloud-eu/reva/v3/pkg/ctx"
	revajwt "github.com/opencloud-eu/reva/v3/pkg/token/manager/jwt"

	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
)

// OpenCloud's account middleware and this service must share reva's
// context key. A digest bump that moves ExtractAccountUUID to a new
// major of reva otherwise answers 401 ("no user in request context")
// to every signed-in call.
func TestExtractAccountUUIDReachesSynaplanAuth(t *testing.T) {
	const secret = "test-jwt-secret"
	manager, err := revajwt.New(map[string]any{
		"secret":  secret,
		"expires": int64(60),
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.MintToken(t.Context(), &userpb.User{
		Id:       &userpb.UserId{OpaqueId: "ada-id", Idp: "https://idp.example"},
		Username: "ada",
	}, map[string]*authpb.Scope{
		"user": {Role: authpb.Role_ROLE_OWNER},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawUser string
	handler := middleware.ExtractAccountUUID(
		account.Logger(log.NopLogger()),
		account.JWTSecret(secret),
	)(synaplanauth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := revactx.ContextGetUser(r.Context())
		if !ok || user.GetId() == nil {
			t.Error("reva user missing after both middlewares")
			return
		}
		sawUser = user.GetId().GetOpaqueId()
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/synaplan/me", nil)
	req.Header.Set(revactx.TokenHeader, token)
	req.Header.Set("Authorization", "Bearer oidc-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if sawUser != "ada-id" {
		t.Fatalf("user %q", sawUser)
	}
}
