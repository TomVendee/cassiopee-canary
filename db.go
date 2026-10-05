package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	// DBPeriod est l'intervalle entre deux tours de surveillance de la base.
	DBPeriod = 5 * time.Second
	// StepTimeout borne chaque opération sur la base : une base suspendue ne
	// doit jamais bloquer la page.
	StepTimeout = 5 * time.Second
)

// Store est ce que l'app attend d'une base, quel que soit le moteur. En Go, un
// type implémente une interface sans le déclarer : il suffit qu'il ait ces
// méthodes. sqlStore (PostgreSQL, MariaDB) et mongoStore le font.
type Store interface {
	Engine() string
	Ping(ctx context.Context) (version string, err error)
	EnsureSchema(ctx context.Context) error      // crée canary_hits si absente
	Write(ctx context.Context, pod string) error // une ligne dans canary_hits
	Count(ctx context.Context) (rows int64, last time.Time, err error)
	TryCredentials(ctx context.Context, user, password string) CredentialReport
	IsAuthError(err error) bool // mot de passe refusé ?
	Close() error
}

// StepResult est le résultat d'une étape d'un test.
type StepResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// stepOf convertit une erreur en StepResult.
func stepOf(err error) StepResult {
	if err != nil {
		return StepResult{Error: err.Error()}
	}
	return StepResult{OK: true}
}

// notTried marque les étapes sautées parce que la connexion a échoué.
var notTried = StepResult{Error: "non tenté"}

// CredentialReport dit ce qu'un couple utilisateur / mot de passe a le droit
// de faire : c'est la mesure réelle des permissions readOnly, readWrite, admin.
type CredentialReport struct {
	Connect StepResult `json:"connect"`
	Read    StepResult `json:"read"`
	Write   StepResult `json:"write"`
	DDL     StepResult `json:"ddl"`
}

// DBStatus est ce que la page affiche de la base.
type DBStatus struct {
	Engine     string    `json:"engine"`
	Version    string    `json:"version"`
	User       string    `json:"user"`
	Database   string    `json:"database"`
	Reachable  bool      `json:"reachable"`
	Since      time.Time `json:"since"` // début de l'état courant (joignable ou non)
	LastError  string    `json:"lastError"`
	AuthFailed bool      `json:"authFailed"`
	WriteOK    bool      `json:"writeOk"`
	Rows       int64     `json:"rows"`
	LastWrite  time.Time `json:"lastWrite"`
}

// Open prépare l'accès à la base sans la contacter : l'app doit démarrer même
// si la base n'existe pas encore ou est suspendue.
func Open(t DBTarget) (Store, error) {
	switch t.Engine {
	case EnginePostgres:
		return openSQL(t, postgresDialect)
	default:
		return nil, fmt.Errorf("moteur %q pas encore pris en charge", t.Engine)
	}
}

// Monitor surveille la base en tâche de fond et garde son dernier état.
type Monitor struct {
	store  Store
	pod    string
	now    func() time.Time
	mu     sync.Mutex
	status DBStatus
}

// NewMonitor crée la surveillance ; la base est réputée injoignable tant
// qu'un premier tour n'a pas réussi.
func NewMonitor(s Store, t DBTarget, pod string, now func() time.Time) *Monitor {
	return &Monitor{
		store: s,
		pod:   pod,
		now:   now,
		status: DBStatus{
			Engine:   t.Engine,
			User:     t.User,
			Database: t.Database,
			Since:    now(),
		},
	}
}

// Run lance un tour tout de suite, puis toutes les every, jusqu'à
// l'annulation de ctx. À appeler dans sa propre goroutine : go m.Run(…).
func (m *Monitor) Run(ctx context.Context, every time.Duration) {
	m.Tick(ctx)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Tick(ctx)
		}
	}
}

// Tick fait un tour : ping, puis à chaque (re)connexion création de la table
// et écriture d'une ligne, ce qui prouve l'écriture ; sinon simple comptage.
// Les appels à la base se font hors du verrou, pour que la page ne les attende
// jamais.
func (m *Monitor) Tick(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, StepTimeout)
	defer cancel()

	version, err := m.store.Ping(ctx)
	if err != nil {
		authFailed := m.store.IsAuthError(err)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.status.Reachable {
			m.status.Since = m.now()
		}
		m.status.Reachable = false
		m.status.LastError = err.Error()
		m.status.AuthFailed = authFailed
		return
	}

	m.mu.Lock()
	reconnected := !m.status.Reachable
	m.mu.Unlock()

	var writeErr error
	if reconnected {
		writeErr = m.store.EnsureSchema(ctx)
		if writeErr == nil {
			writeErr = m.store.Write(ctx, m.pod)
		}
	}
	rows, last, countErr := m.store.Count(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if reconnected {
		m.status.Since = m.now()
		m.status.WriteOK = writeErr == nil
	}
	m.status.Reachable = true
	m.status.AuthFailed = false
	m.status.Version = version
	m.status.LastError = ""
	if writeErr != nil {
		m.status.LastError = writeErr.Error()
	}
	if countErr == nil {
		m.status.Rows, m.status.LastWrite = rows, last
	} else if m.status.LastError == "" {
		m.status.LastError = countErr.Error()
	}
}

// Status renvoie une copie du dernier état connu.
func (m *Monitor) Status() DBStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Write écrit une ligne à la demande (bouton « Écrire » de la page).
func (m *Monitor) Write(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, StepTimeout)
	defer cancel()
	if err := m.store.Write(ctx, m.pod); err != nil {
		return err
	}
	if rows, last, err := m.store.Count(ctx); err == nil {
		m.mu.Lock()
		m.status.Rows, m.status.LastWrite = rows, last
		m.mu.Unlock()
	}
	return nil
}

// TryCredentials teste un couple utilisateur / mot de passe sur la même base.
func (m *Monitor) TryCredentials(ctx context.Context, user, password string) CredentialReport {
	return m.store.TryCredentials(ctx, user, password)
}
