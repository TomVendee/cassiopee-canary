package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

var errFakeAuth = errors.New("authentification refusée")

// fakeStore est une base en mémoire dont le test règle les réponses.
type fakeStore struct {
	mu          sync.Mutex
	pingErr     error
	writeErr    error
	schemaCalls int
	writes      int
	credCalls   int
	report      CredentialReport
}

func (f *fakeStore) Engine() string { return EnginePostgres }

func (f *fakeStore) Ping(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "PostgreSQL 17.0", f.pingErr
}

func (f *fakeStore) EnsureSchema(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schemaCalls++
	return nil
}

func (f *fakeStore) Write(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes++
	return nil
}

func (f *fakeStore) Count(context.Context) (int64, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(f.writes), time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), nil
}

func (f *fakeStore) TryCredentials(context.Context, string, string) CredentialReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credCalls++
	return f.report
}

func (f *fakeStore) IsAuthError(err error) bool { return errors.Is(err, errFakeAuth) }
func (f *fakeStore) Close() error               { return nil }

func (f *fakeStore) set(pingErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pingErr = pingErr
}

var testTarget = DBTarget{Engine: EnginePostgres, Host: "h", Port: 5432, User: "postgres", Password: "pw", Database: "app"}

func TestOpenDoesNotDial(t *testing.T) {
	target, err := ParseDatabaseURL("postgresql://u:p@127.0.0.1:1/app")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s, err := Open(*target)
	if err != nil {
		t.Fatalf("Open = %v, attendu nil (connexion paresseuse)", err)
	}
	defer s.Close()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("Open a pris %v : il ne doit pas contacter la base", d)
	}
}

func TestMonitorRecovers(t *testing.T) {
	clock := newFakeClock()
	f := &fakeStore{pingErr: errors.New("connection refused")}
	m := NewMonitor(f, testTarget, "pod-a", clock.Now)
	t0 := clock.Now()
	ctx := context.Background()

	m.Tick(ctx)
	s := m.Status()
	if s.Reachable || !s.Since.Equal(t0) || s.LastError == "" {
		t.Fatalf("t0 : %+v", s)
	}

	clock.Advance(5 * time.Second)
	t1 := clock.Now()
	f.set(nil)
	m.Tick(ctx)
	s = m.Status()
	if !s.Reachable || !s.Since.Equal(t1) || !s.WriteOK || s.LastError != "" {
		t.Fatalf("t1 : %+v", s)
	}
	if f.schemaCalls != 1 || f.writes != 1 {
		t.Errorf("t1 : schéma %d fois, %d écritures ; attendu 1 et 1", f.schemaCalls, f.writes)
	}
	if s.Version != "PostgreSQL 17.0" || s.User != "postgres" || s.Database != "app" || s.Engine != EnginePostgres {
		t.Errorf("t1 identité : %+v", s)
	}

	clock.Advance(5 * time.Second)
	m.Tick(ctx)
	if f.writes != 1 {
		t.Errorf("t2 : %d écritures, attendu 1 (pas d'écriture sans reconnexion)", f.writes)
	}
	if s = m.Status(); s.Rows != 1 || !s.Since.Equal(t1) {
		t.Errorf("t2 : %+v", s)
	}

	clock.Advance(5 * time.Second)
	f.set(errFakeAuth)
	m.Tick(ctx)
	if s = m.Status(); s.Reachable || !s.AuthFailed {
		t.Errorf("t3 : %+v", s)
	}

	clock.Advance(5 * time.Second)
	f.set(nil)
	m.Tick(ctx)
	if s = m.Status(); !s.Reachable || s.AuthFailed {
		t.Errorf("t4 : %+v", s)
	}
	if f.writes != 2 {
		t.Errorf("t4 : %d écritures, attendu 2 (nouvelle écriture à la reconnexion)", f.writes)
	}
}

func dbDeps(f *fakeStore) Deps {
	d := testDeps(Config{})
	if f != nil {
		d.DB = NewMonitor(f, testTarget, d.Pod, time.Now)
	}
	return d
}

func TestCredentialsRouteValidation(t *testing.T) {
	f := &fakeStore{report: CredentialReport{Connect: StepResult{OK: true}, Read: StepResult{OK: true}}}
	h := NewServer(dbDeps(f))

	if w := do(h, "POST", "/api/db/credentials", []byte(`{"user":"","password":"x"}`)); w.Code != 400 {
		t.Errorf("utilisateur vide = %d, attendu 400", w.Code)
	}
	if w := do(h, "POST", "/api/db/credentials", []byte(`{"user":"a","password":""}`)); w.Code != 400 {
		t.Errorf("mot de passe vide = %d, attendu 400", w.Code)
	}
	big := `{"user":"a","password":"` + strings.Repeat("x", 2048) + `"}`
	if w := do(h, "POST", "/api/db/credentials", []byte(big)); w.Code != 413 {
		t.Errorf("corps de 2 Ko = %d, attendu 413", w.Code)
	}
	if w := do(h, "POST", "/api/db/credentials", []byte(`pas du json`)); w.Code != 400 {
		t.Errorf("JSON invalide = %d, attendu 400", w.Code)
	}
	if f.credCalls != 0 {
		t.Fatalf("TryCredentials appelé %d fois sur des entrées invalides", f.credCalls)
	}

	w := do(h, "POST", "/canary/api/db/credentials", []byte(`{"user":"lecteur","password":"x"}`))
	if w.Code != 200 {
		t.Fatalf("entrée valide = %d", w.Code)
	}
	var got CredentialReport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Connect.OK || !got.Read.OK || got.Write.OK || f.credCalls != 1 {
		t.Errorf("rapport %+v, appels %d", got, f.credCalls)
	}
}

func TestDBRoutesWithoutDB(t *testing.T) {
	h := NewServer(dbDeps(nil))
	if w := do(h, "POST", "/api/db/write", nil); w.Code != 409 {
		t.Errorf("écriture sans base = %d, attendu 409", w.Code)
	}
	if w := do(h, "POST", "/api/db/credentials", []byte(`{"user":"a","password":"b"}`)); w.Code != 409 {
		t.Errorf("identifiants sans base = %d, attendu 409", w.Code)
	}
}

func TestWriteRoute(t *testing.T) {
	f := &fakeStore{}
	h := NewServer(dbDeps(f))
	if w := do(h, "POST", "/api/db/write", nil); w.Code != 204 || f.writes != 1 {
		t.Errorf("écriture = %d, %d lignes", w.Code, f.writes)
	}
	f.writeErr = errors.New("permission denied")
	w := do(h, "POST", "/api/db/write", nil)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "permission denied") {
		t.Errorf("écriture en échec = %d %q", w.Code, w.Body.String())
	}
}
