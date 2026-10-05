package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// dialect regroupe ce qui diffère entre PostgreSQL et MariaDB ; tout le reste
// passe par database/sql, la couche commune de la bibliothèque standard.
type dialect struct {
	engine      string
	driver      string                // nom du pilote enregistré auprès de database/sql
	dsn         func(DBTarget) string // chaîne de connexion attendue par ce pilote
	createSQL   string
	insertSQL   string // un paramètre : le nom du pod
	versionSQL  string
	isAuthError func(error) bool
}

// sqlStore implémente Store pour les moteurs SQL.
type sqlStore struct {
	d      dialect
	target DBTarget
	db     *sql.DB
}

// openSQL prépare un pool de connexions. sql.Open ne contacte pas la base :
// la première connexion se fait au premier appel.
func openSQL(t DBTarget, d dialect) (*sqlStore, error) {
	db, err := sql.Open(d.driver, d.dsn(t))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	return &sqlStore{d: d, target: t, db: db}, nil
}

func (s *sqlStore) Engine() string { return s.d.engine }

func (s *sqlStore) Ping(ctx context.Context) (string, error) {
	if err := s.db.PingContext(ctx); err != nil {
		return "", err
	}
	var version string
	err := s.db.QueryRowContext(ctx, s.d.versionSQL).Scan(&version)
	return version, err
}

func (s *sqlStore) EnsureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, s.d.createSQL)
	return err
}

func (s *sqlStore) Write(ctx context.Context, pod string) error {
	_, err := s.db.ExecContext(ctx, s.d.insertSQL, pod)
	return err
}

func (s *sqlStore) Count(ctx context.Context) (int64, time.Time, error) {
	var n int64
	var last sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*), MAX(created_at) FROM canary_hits").Scan(&n, &last)
	return n, last.Time, err
}

// TryCredentials ouvre une connexion à part avec ces identifiants, sur le même
// hôte et la même base, et tente quatre opérations de droits croissants.
func (s *sqlStore) TryCredentials(ctx context.Context, user, password string) CredentialReport {
	other, err := openSQL(s.target.WithCredentials(user, password), s.d)
	if err != nil {
		return CredentialReport{Connect: stepOf(err), Read: notTried, Write: notTried, DDL: notTried}
	}
	defer other.Close()
	other.db.SetMaxOpenConns(1)

	step := func(f func(context.Context) error) StepResult {
		c, cancel := context.WithTimeout(ctx, StepTimeout)
		defer cancel()
		return stepOf(f(c))
	}

	r := CredentialReport{Connect: step(other.db.PingContext)}
	if !r.Connect.OK {
		r.Read, r.Write, r.DDL = notTried, notTried, notTried
		return r
	}
	r.Read = step(func(c context.Context) error {
		var n int64
		return other.db.QueryRowContext(c, "SELECT COUNT(*) FROM canary_hits").Scan(&n)
	})
	r.Write = step(func(c context.Context) error {
		_, err := other.db.ExecContext(c, s.d.insertSQL, "credentials-test")
		return err
	})
	r.DDL = step(func(c context.Context) error {
		// Nom unique : une table restée d'un essai interrompu ne fausse pas le résultat.
		table := fmt.Sprintf("canary_ddl_probe_%d", time.Now().UnixNano()%1_000_000_000)
		if _, err := other.db.ExecContext(c, "CREATE TABLE "+table+" (id INT)"); err != nil {
			return err
		}
		_, err := other.db.ExecContext(c, "DROP TABLE "+table)
		return err
	})
	return r
}

func (s *sqlStore) IsAuthError(err error) bool { return err != nil && s.d.isAuthError(err) }

func (s *sqlStore) Close() error { return s.db.Close() }
