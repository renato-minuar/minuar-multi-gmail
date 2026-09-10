package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
)

type refreshServer struct {
	srv   *httptest.Server
	calls int
	// respond decides the response for call n (1-based)
	respond func(n int, w http.ResponseWriter)
}

func newRefreshServer(t *testing.T, respond func(n int, w http.ResponseWriter)) *refreshServer {
	rs := &refreshServer{respond: respond}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		rs.calls++
		w.Header().Set("Content-Type", "application/json")
		rs.respond(rs.calls, w)
	})
	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *refreshServer) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{AuthURL: rs.srv.URL + "/auth", TokenURL: rs.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
}

func okToken(w http.ResponseWriter, refresh string) {
	resp := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
	if refresh != "" {
		resp["refresh_token"] = refresh
	}
	json.NewEncoder(w).Encode(resp)
}

func TestNewTokenSourceMissingTokenIsReauth(t *testing.T) {
	_, err := NewTokenSource(context.Background(), TokenSourceOptions{
		Creds: ClientCreds{ID: "id", Secret: "s"}, Store: secrets.NewMem(), Alias: "work", Email: "control@example.com",
	})
	var re *ReauthError
	if !errors.As(err, &re) || re.Alias != "work" {
		t.Fatalf("err = %v, want ReauthError", err)
	}
	if !strings.Contains(err.Error(), `account "work" (control@example.com) needs re-login: run minuar-multi-gmail add-account work --replace`) {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestTokenSourcePersistsRotatedRefreshToken(t *testing.T) {
	rs := newRefreshServer(t, func(n int, w http.ResponseWriter) { okToken(w, "1//0gRotated") })
	st := secrets.NewMem()
	st.Set(secrets.RefreshTokenKey("work"), "1//0gOriginal")
	ts, err := NewTokenSource(context.Background(), TokenSourceOptions{
		Creds: ClientCreds{ID: "id", Secret: "s"}, Store: st, Alias: "work", Email: "e", Endpoint: rs.endpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil || tok.AccessToken != "at" {
		t.Fatalf("tok = %+v err %v", tok, err)
	}
	if got, _ := st.Get(secrets.RefreshTokenKey("work")); got != "1//0gRotated" {
		t.Fatalf("stored refresh token = %q, want rotated value", got)
	}
	// Second call reuses the cached access token: no extra refresh.
	if _, err := ts.Token(); err != nil || rs.calls != 1 {
		t.Fatalf("calls = %d err %v", rs.calls, err)
	}
}

func TestTokenSourceSurvivesCallerContextCancellation(t *testing.T) {
	rs := newRefreshServer(t, func(n int, w http.ResponseWriter) { okToken(w, "") })
	st := secrets.NewMem()
	st.Set(secrets.RefreshTokenKey("work"), "1//0gOriginal")
	ctx, cancel := context.WithCancel(context.Background())
	ts, err := NewTokenSource(ctx, TokenSourceOptions{
		Creds: ClientCreds{ID: "id", Secret: "s"}, Store: st, Alias: "work", Email: "e", Endpoint: rs.endpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The caller (e.g. one MCP tool call) is done and cancels its context.
	// A token source cached per alias must keep refreshing afterward.
	cancel()
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token() after caller context canceled: %v", err)
	}
}

func TestTokenSourceKeepsStoredTokenWhenNotRotated(t *testing.T) {
	rs := newRefreshServer(t, func(n int, w http.ResponseWriter) { okToken(w, "") })
	st := secrets.NewMem()
	st.Set(secrets.RefreshTokenKey("work"), "1//0gOriginal")
	ts, _ := NewTokenSource(context.Background(), TokenSourceOptions{
		Creds: ClientCreds{ID: "id", Secret: "s"}, Store: st, Alias: "work", Email: "e", Endpoint: rs.endpoint(),
	})
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(secrets.RefreshTokenKey("work")); got != "1//0gOriginal" {
		t.Fatalf("stored = %q", got)
	}
}

func TestTokenSourceInvalidGrantIsReauth(t *testing.T) {
	rs := newRefreshServer(t, func(n int, w http.ResponseWriter) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	})
	st := secrets.NewMem()
	st.Set(secrets.RefreshTokenKey("work"), "1//0gOriginal")
	ts, _ := NewTokenSource(context.Background(), TokenSourceOptions{
		Creds: ClientCreds{ID: "id", Secret: "s"}, Store: st, Alias: "work", Email: "control@example.com", Endpoint: rs.endpoint(),
	})
	_, err := ts.Token()
	var re *ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want ReauthError", err)
	}
}

func TestClassifyRefreshErrorPassesOthersThrough(t *testing.T) {
	plain := errors.New("network down")
	if got := ClassifyRefreshError(plain, "work", "e"); got != plain {
		t.Fatalf("got %v", got)
	}
	re := &oauth2.RetrieveError{ErrorCode: "invalid_grant", Response: &http.Response{StatusCode: 400}}
	var want *ReauthError
	if !errors.As(ClassifyRefreshError(re, "work", "e"), &want) {
		t.Fatal("invalid_grant not classified")
	}
	unauth := &oauth2.RetrieveError{Response: &http.Response{StatusCode: 401}}
	if !errors.As(ClassifyRefreshError(unauth, "work", "e"), &want) {
		t.Fatal("401 not classified")
	}
	server := &oauth2.RetrieveError{ErrorCode: "temporarily_unavailable", Response: &http.Response{StatusCode: 503}}
	if errors.As(ClassifyRefreshError(server, "work", "e"), &want) {
		t.Fatal("503 must not be classified as reauth")
	}
}

func TestRevokePostsToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotToken = r.PostForm.Get("token")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if err := Revoke(context.Background(), srv.URL, "1//0gTok"); err != nil {
		t.Fatal(err)
	}
	if gotToken != "1//0gTok" {
		t.Fatalf("token = %q", gotToken)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer bad.Close()
	if err := Revoke(context.Background(), bad.URL, "x"); err == nil {
		t.Fatal("400 must be an error")
	}
}
