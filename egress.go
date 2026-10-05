package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// EgressResult dit ce que le pod peut joindre : son DNS interne, le DNS
// public, et Internet en HTTPS (que SiOps peut fermer par NetworkPolicy).
type EgressResult struct {
	InternalHost string     `json:"internalHost"`
	InternalDNS  StepResult `json:"internalDns"`
	ExternalDNS  StepResult `json:"externalDns"`
	HTTPS        StepResult `json:"https"`
	CheckedAt    time.Time  `json:"checkedAt"`
}

// Resolver est la partie de net.Resolver dont on a besoin ; les tests en
// fournissent une version factice.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// CheckEgress mesure les trois accès. Le client HTTP porte son propre délai.
func CheckEgress(ctx context.Context, r Resolver, client *http.Client, internalHost, externalURL string) EgressResult {
	res := EgressResult{InternalHost: internalHost, CheckedAt: time.Now()}

	_, err := r.LookupHost(ctx, internalHost)
	res.InternalDNS = stepOf(err)

	u, err := url.Parse(externalURL)
	if err != nil {
		res.ExternalDNS, res.HTTPS = stepOf(err), notTried
		return res
	}
	_, err = r.LookupHost(ctx, u.Hostname())
	res.ExternalDNS = stepOf(err)

	res.HTTPS = stepOf(httpGet(ctx, client, externalURL))
	return res
}

func httpGet(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			return errors.New("délai dépassé : sortie probablement fermée")
		}
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("statut %d", resp.StatusCode)
	}
	return nil
}

// EgressWatcher refait la mesure en tâche de fond et garde la dernière.
type EgressWatcher struct {
	check func(context.Context) EgressResult
	mu    sync.Mutex
	last  *EgressResult
}

// NewEgressWatcher prend la fonction de mesure à répéter.
func NewEgressWatcher(check func(context.Context) EgressResult) *EgressWatcher {
	return &EgressWatcher{check: check}
}

// Run mesure tout de suite, puis toutes les every, jusqu'à l'annulation de ctx.
func (w *EgressWatcher) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		res := w.check(ctx)
		w.mu.Lock()
		w.last = &res
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Last renvoie la dernière mesure, et false s'il n'y en a pas encore.
func (w *EgressWatcher) Last() (EgressResult, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		return EgressResult{}, false
	}
	return *w.last, true
}
