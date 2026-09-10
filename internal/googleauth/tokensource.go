package googleauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
)

// ReauthError means the stored refresh token is missing or rejected and
// the account needs a fresh browser login.
type ReauthError struct {
	Alias  string
	Email  string
	Reason string
}

func (e *ReauthError) Error() string {
	msg := fmt.Sprintf("account %q (%s) needs re-login: run minuar-multi-gmail add-account %s --replace", e.Alias, e.Email, e.Alias)
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg
}

type TokenSourceOptions struct {
	Creds    ClientCreds
	Store    secrets.Store
	Alias    string
	Email    string
	Endpoint oauth2.Endpoint
}

type persistingSource struct {
	inner oauth2.TokenSource
	store secrets.Store
	key   string
	alias string
	email string
	mu    sync.Mutex
	last  string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, ClassifyRefreshError(err, p.alias, p.email)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.RefreshToken != "" && tok.RefreshToken != p.last {
		if err := p.store.Set(p.key, tok.RefreshToken); err != nil {
			return nil, fmt.Errorf("persist rotated refresh token for %s: %w", p.alias, err)
		}
		p.last = tok.RefreshToken
	}
	return tok, nil
}

// NewTokenSource returns a cached, self-refreshing token source for one
// account. A missing stored token is a ReauthError.
func NewTokenSource(ctx context.Context, o TokenSourceOptions) (oauth2.TokenSource, error) {
	key := secrets.RefreshTokenKey(o.Alias)
	rt, err := o.Store.Get(key)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, &ReauthError{Alias: o.Alias, Email: o.Email, Reason: "no stored token"}
		}
		return nil, fmt.Errorf("read refresh token for %s: %w", o.Alias, err)
	}
	cfg := NewConfig(o.Creds, "", o.Endpoint)
	// The returned source is cached and reused across future calls, long
	// after this constructor's ctx is gone (e.g. across MCP tool calls).
	// Drop cancellation so a refresh doesn't fail with "context canceled"
	// once the caller that created the source has moved on, while still
	// carrying any context values (such as oauth2.HTTPClient) forward.
	refreshCtx := context.WithoutCancel(ctx)
	inner := cfg.TokenSource(refreshCtx, &oauth2.Token{RefreshToken: rt})
	return &persistingSource{inner: inner, store: o.Store, key: key, alias: o.Alias, email: o.Email, last: rt}, nil
}

// ClassifyRefreshError turns Google's "this grant is dead" answers into a
// ReauthError and leaves every other error untouched.
func ClassifyRefreshError(err error, alias, email string) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return err
	}
	status := 0
	if re.Response != nil {
		status = re.Response.StatusCode
	}
	if re.ErrorCode == "invalid_grant" || status == http.StatusUnauthorized {
		reason := re.ErrorCode
		if re.ErrorDescription != "" {
			reason = re.ErrorDescription
		}
		return &ReauthError{Alias: alias, Email: email, Reason: reason}
	}
	return err
}

// Revoke tells Google to forget the token. Best effort for remove-account.
func Revoke(ctx context.Context, revokeURL, token string) error {
	if revokeURL == "" {
		revokeURL = RevokeURL
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke: Google answered %s", resp.Status)
	}
	return nil
}
