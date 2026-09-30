package googleauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

var ErrNoRefreshToken = errors.New("Google returned no refresh token: remove minuar-multi-gmail at https://myaccount.google.com/permissions for this account and run add-account again")

type LoginOptions struct {
	Creds    ClientCreds
	OpenURL  func(url string) error
	Timeout  time.Duration
	Endpoint oauth2.Endpoint
	Notify   io.Writer
}

type callbackResult struct {
	code string
	err  error
}

// Login runs the loopback PKCE flow and returns a token that carries a
// refresh token. It serves exactly one callback request.
func Login(ctx context.Context, o LoginOptions) (*oauth2.Token, error) {
	if o.Creds.ID == "" || o.Creds.Secret == "" {
		return nil, errors.New("login: OAuth client credentials are empty")
	}
	if o.OpenURL == nil {
		return nil, errors.New("login: OpenURL is required")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("login: listen on loopback: %w", err)
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr().String())
	cfg := NewConfig(o.Creds, redirect, o.Endpoint)

	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("login: random state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	results := make(chan callbackResult, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res callbackResult
		switch {
		case q.Get("state") != state:
			res.err = errors.New("login: state mismatch in callback; refusing the code")
		case q.Get("error") != "":
			res.err = fmt.Errorf("login: Google returned error %q", q.Get("error"))
		case q.Get("code") == "":
			res.err = errors.New("login: callback without code")
		default:
			res.code = q.Get("code")
		}
		if res.err != nil {
			http.Error(w, res.err.Error(), http.StatusBadRequest)
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, "minuar-multi-gmail: login complete. You can close this tab.\n")
		}
		once.Do(func() { results <- res })
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if o.Notify != nil {
		fmt.Fprintf(o.Notify, "Opening your browser. If it does not open, visit:\n%s\n", authURL)
	}
	if err := o.OpenURL(authURL); err != nil && o.Notify != nil {
		// The URL is already on screen. A machine without a browser
		// (a server over SSH) completes the login by hand.
		fmt.Fprintf(o.Notify, "Could not open a browser (%v). Open the URL above yourself.\n", err)
	}

	var res callbackResult
	select {
	case res = <-results:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("login: timed out after %s waiting for the browser", timeout)
		}
		return nil, fmt.Errorf("login: %w", ctx.Err())
	}
	if res.err != nil {
		return nil, res.err
	}
	tok, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("login: exchange code: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, ErrNoRefreshToken
	}
	return tok, nil
}
