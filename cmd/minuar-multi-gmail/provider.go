package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"google.golang.org/api/option"
)

// provider builds one gmail.Service per alias and keeps it for the life
// of the process. Token sources use context.Background() on purpose: a
// tool call's context ends with the call, the token source must not.
type provider struct {
	mu        sync.Mutex
	store     secrets.Store
	configDir string
	creds     *googleauth.ClientCreds
	services  map[string]gmail.Service
}

func newProvider(store secrets.Store, configDir string) *provider {
	return &provider{store: store, configDir: configDir, services: map[string]gmail.Service{}}
}

func (p *provider) loadConfig() (*config.File, error) { return config.Load(p.configDir) }

func (p *provider) service(ctx context.Context, alias string) (gmail.Service, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if svc, ok := p.services[alias]; ok {
		return svc, nil
	}
	if p.creds == nil {
		c, err := googleauth.LoadClientCreds(p.store)
		if err != nil {
			return nil, err
		}
		p.creds = &c
	}
	cfg, err := p.loadConfig()
	if err != nil {
		return nil, err
	}
	acct, ok := cfg.Find(alias)
	if !ok {
		return nil, fmt.Errorf("unknown account %q", alias)
	}
	ts, err := googleauth.NewTokenSource(context.Background(), googleauth.TokenSourceOptions{
		Creds: *p.creds, Store: p.store, Alias: acct.Alias, Email: acct.Email,
	})
	if err != nil {
		return nil, err
	}
	svc, err := gmail.New(ctx, acct.Alias, acct.Email, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	p.services[alias] = svc
	return svc, nil
}
