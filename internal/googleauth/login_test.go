package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeGoogle is a token endpoint that records the exchange request.
type fakeGoogle struct {
	srv          *httptest.Server
	refreshToken string // returned in the exchange; "" omits it
	exchanges    []url.Values
}

func newFakeGoogle(t *testing.T, refreshToken string) *fakeGoogle {
	f := &fakeGoogle{refreshToken: refreshToken}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.exchanges = append(f.exchanges, r.PostForm)
		resp := map[string]any{"access_token": "at-1", "token_type": "Bearer", "expires_in": 3600}
		if f.refreshToken != "" {
			resp["refresh_token"] = f.refreshToken
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
}

// browser simulates the user: it inspects the auth URL and hits the
// loopback redirect with the given state override ("" keeps the real one).
func browser(t *testing.T, stateOverride string, sawURL *url.Values) func(string) error {
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		*sawURL = q
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		if stateOverride != "" {
			state = stateOverride
		}
		go func() {
			resp, err := http.Get(redirect + "?state=" + url.QueryEscape(state) + "&code=the-code")
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoginHappyPath(t *testing.T) {
	g := newFakeGoogle(t, "1//0gRefresh")
	var saw url.Values
	tok, err := Login(context.Background(), LoginOptions{
		Creds:    ClientCreds{ID: "id", Secret: "sec"},
		OpenURL:  browser(t, "", &saw),
		Endpoint: g.endpoint(),
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "1//0gRefresh" {
		t.Fatalf("token = %+v", tok)
	}
	if saw.Get("access_type") != "offline" || saw.Get("prompt") != "consent" ||
		saw.Get("code_challenge_method") != "S256" || saw.Get("code_challenge") == "" ||
		saw.Get("scope") != Scope || !strings.HasPrefix(saw.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatalf("auth url params = %v", saw)
	}
	if len(g.exchanges) != 1 {
		t.Fatalf("exchanges = %d", len(g.exchanges))
	}
	ex := g.exchanges[0]
	if ex.Get("code") != "the-code" || ex.Get("code_verifier") == "" || ex.Get("grant_type") != "authorization_code" {
		t.Fatalf("exchange form = %v", ex)
	}
}

func TestLoginRejectsStateMismatch(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	var saw url.Values
	_, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"}, OpenURL: browser(t, "wrong-state", &saw),
		Endpoint: g.endpoint(), Timeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v, want state mismatch", err)
	}
	if len(g.exchanges) != 0 {
		t.Fatal("code must not be exchanged after a state mismatch")
	}
}

func TestLoginRequiresRefreshToken(t *testing.T) {
	g := newFakeGoogle(t, "")
	var saw url.Values
	_, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"}, OpenURL: browser(t, "", &saw),
		Endpoint: g.endpoint(), Timeout: 5 * time.Second,
	})
	if !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("err = %v, want ErrNoRefreshToken", err)
	}
}

func TestLoginTimesOut(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	_, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"}, OpenURL: func(string) error { return nil },
		Endpoint: g.endpoint(), Timeout: 200 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

// TestLoginTimesOutDuringStalledExchange guards against Login hanging
// forever when the callback arrives promptly but the token endpoint never
// answers the code exchange: the whole call must still be bounded by
// Timeout, and the loopback listener must be closed once it returns.
func TestLoginTimesOutDuringStalledExchange(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// Drain the body first: net/http only watches for the client
		// giving up (and cancels r.Context()) once the request body has
		// been fully read: https://pkg.go.dev/net/http#Request.Context.
		r.ParseForm()
		<-r.Context().Done() // never respond; only unblocks when the client gives up
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	endpoint := oauth2.Endpoint{AuthURL: srv.URL + "/auth", TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}

	var saw url.Values
	start := time.Now()
	_, err := Login(context.Background(), LoginOptions{
		Creds:    ClientCreds{ID: "id", Secret: "sec"},
		OpenURL:  browser(t, "", &saw),
		Endpoint: endpoint,
		Timeout:  200 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("want an error from a stalled token endpoint")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Login took %s, want it bounded by Timeout", elapsed)
	}

	redirect := saw.Get("redirect_uri")
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if conn, dialErr := net.DialTimeout("tcp", u.Host, time.Second); dialErr == nil {
		conn.Close()
		t.Fatal("loopback listener still accepting connections after Login returned")
	}
}

func TestLoginOpenURLFailure(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	_, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"}, OpenURL: func(string) error { return errors.New("no browser") },
		Endpoint: g.endpoint(), Timeout: 200 * time.Millisecond,
	})
	// With no Notify writer and a failed open, login continues and times out
	// waiting for the callback that never arrives.
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

// A machine without a browser (a server over SSH) still completes the
// login: the URL is printed, the open fails, the callback arrives.
func TestLoginContinuesWhenBrowserCannotOpen(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	var saw url.Values
	open := browser(t, "", &saw)
	var notes strings.Builder
	tok, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"},
		OpenURL: func(u string) error {
			open(u) // the test's stand-in for the user pasting the URL
			return errors.New("exec: \"xdg-open\": executable file not found in $PATH")
		},
		Endpoint: g.endpoint(), Timeout: 5 * time.Second, Notify: &notes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "rt" {
		t.Fatalf("token = %+v", tok)
	}
	if !strings.Contains(notes.String(), "Could not open a browser") || !strings.Contains(notes.String(), "xdg-open") {
		t.Fatalf("notify = %q", notes.String())
	}
}

// The user on a server needs the port to forward, so Login prints it.
func TestLoginNotifiesThePortForSSHForwarding(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	var saw url.Values
	var notes strings.Builder
	if _, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"}, OpenURL: browser(t, "", &saw),
		Endpoint: g.endpoint(), Timeout: 5 * time.Second, Notify: &notes,
	}); err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(saw.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	port := redirect.Port()
	want := "ssh -L " + port + ":127.0.0.1:" + port
	if port == "" || !strings.Contains(notes.String(), want) {
		t.Fatalf("notify lacks %q: %q", want, notes.String())
	}
}
