//go:build integration

// Tests contre de vraies bases. Ils ne tournent qu'avec le build tag
// « integration » et les variables CANARY_TEST_*_URL (voir compose.yaml) :
//
//	go test -race -tags integration ./...
package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

const (
	readerUser     = "lecteur"
	readerPassword = "lecteur-pw"
)

// adminTarget lit l'URL d'administration d'un moteur, ou saute le test.
func adminTarget(t *testing.T, env string) DBTarget {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s non défini", env)
	}
	target, err := ParseDatabaseURL(raw)
	if err != nil {
		t.Fatalf("%s : %v", env, err)
	}
	return *target
}

// runStoreScenario joue le même scénario sur chaque moteur : écriture et
// relecture, utilisateur en lecture seule, mauvais mot de passe.
func runStoreScenario(t *testing.T, admin DBTarget, createReader func(t *testing.T)) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s, err := Open(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping : %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema : %v", err)
	}
	if err := s.Write(ctx, "pod-test"); err != nil {
		t.Fatalf("Write : %v", err)
	}
	rows, last, err := s.Count(ctx)
	if err != nil || rows < 1 || last.IsZero() {
		t.Fatalf("Count = %d, %v, %v", rows, last, err)
	}

	createReader(t)
	r := s.TryCredentials(ctx, readerUser, readerPassword)
	if !r.Connect.OK || !r.Read.OK {
		t.Errorf("lecteur : connexion %+v, lecture %+v ; attendues OK", r.Connect, r.Read)
	}
	if r.Write.OK || r.DDL.OK {
		t.Errorf("lecteur : écriture %+v, DDL %+v ; attendues refusées", r.Write, r.DDL)
	}

	bad, err := Open(admin.WithCredentials(admin.User, "mauvais-mot-de-passe"))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	_, err = bad.Ping(ctx)
	if err == nil || !bad.IsAuthError(err) {
		t.Errorf("mauvais mot de passe : err = %v, IsAuthError = %v", err, err != nil && bad.IsAuthError(err))
	}
}

// execSQL exécute des requêtes d'administration avec le compte admin.
func execSQL(t *testing.T, driver, dsn string, queries ...string) {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s : %v", q, err)
		}
	}
}

func TestPostgresIntegration(t *testing.T) {
	admin := adminTarget(t, "CANARY_TEST_POSTGRES_URL")
	runStoreScenario(t, admin, func(t *testing.T) {
		execSQL(t, "pgx", admin.URL(),
			// DROP OWNED retire aussi les droits accordés, sans quoi DROP ROLE échoue.
			"DO $$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = '"+readerUser+"') THEN "+
				"DROP OWNED BY "+readerUser+"; DROP ROLE "+readerUser+"; END IF; END $$",
			"CREATE ROLE "+readerUser+" LOGIN PASSWORD '"+readerPassword+"'",
			"GRANT SELECT ON canary_hits TO "+readerUser,
		)
	})
}
