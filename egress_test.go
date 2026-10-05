package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeResolver résout tout, sauf les hôtes listés dans fail.
type fakeResolver struct{ fail map[string]bool }

func (r fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if r.fail[host] {
		return nil, errors.New("no such host")
	}
	return []string{"10.0.0.1"}, nil
}

func TestCheckEgress(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	ctx := context.Background()

	res := CheckEgress(ctx, fakeResolver{}, ok.Client(), "canary-db-postgresql", ok.URL)
	if !res.InternalDNS.OK || !res.ExternalDNS.OK || !res.HTTPS.OK {
		t.Errorf("tout devrait passer : %+v", res)
	}
	if res.InternalHost != "canary-db-postgresql" || res.CheckedAt.IsZero() {
		t.Errorf("hôte %q, date %v", res.InternalHost, res.CheckedAt)
	}

	res = CheckEgress(ctx, fakeResolver{fail: map[string]bool{"127.0.0.1": true}}, ok.Client(), "h", ok.URL)
	if res.ExternalDNS.OK || !res.InternalDNS.OK {
		t.Errorf("DNS externe en échec attendu : %+v", res)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // répond en 5 s, ou s'arrête dès que le client abandonne
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	start := time.Now()
	res = CheckEgress(ctx, fakeResolver{}, &http.Client{Timeout: 3 * time.Second}, "h", slow.URL)
	if res.HTTPS.OK {
		t.Error("HTTPS devrait échouer sur délai dépassé")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("CheckEgress a pris %v, le délai de 3 s n'est pas respecté", d)
	}
}

func TestEgressWatcher(t *testing.T) {
	w := NewEgressWatcher(func(context.Context) EgressResult {
		return EgressResult{InternalHost: "h", CheckedAt: time.Now()}
	})
	if _, ok := w.Last(); ok {
		t.Error("Last avant tout tour : attendu false")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx, time.Hour)
	defer cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if res, ok := w.Last(); ok && res.InternalHost == "h" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("Run n'a pas fait de premier tour immédiat")
}
