package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// envOf construit une fonction getenv à partir d'une table, comme le ferait
// os.Getenv avec un environnement donné.
func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(envOf(nil))
	if err != nil {
		t.Fatalf("Load() erreur inattendue : %v", err)
	}
	if c.Port != 8080 || !c.PortAllowed {
		t.Errorf("port = %d (autorisé %v), attendu 8080 autorisé", c.Port, c.PortAllowed)
	}
	if c.Mode != "web" {
		t.Errorf("mode = %q, attendu web", c.Mode)
	}
	if c.DB != nil {
		t.Errorf("DB = %+v, attendu nil", c.DB)
	}
	if c.StartupDelay != 0 || c.CronExitCode != 0 {
		t.Errorf("StartupDelay = %v, CronExitCode = %d, attendus 0", c.StartupDelay, c.CronExitCode)
	}
}

func TestLoadErrorsAndWarnings(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string // vide : pas d'erreur attendue
		check   func(t *testing.T, c Config)
	}{
		{name: "port hors NetworkPolicy", vars: map[string]string{"PORT": "3000"},
			check: func(t *testing.T, c Config) {
				if c.Port != 3000 || c.PortAllowed {
					t.Errorf("port = %d autorisé %v, attendu 3000 non autorisé", c.Port, c.PortAllowed)
				}
			}},
		{name: "port 443 autorisé", vars: map[string]string{"PORT": "443"},
			check: func(t *testing.T, c Config) {
				if !c.PortAllowed {
					t.Error("443 devrait être autorisé")
				}
			}},
		{name: "port non numérique", vars: map[string]string{"PORT": "abc"}, wantErr: "PORT"},
		{name: "port nul", vars: map[string]string{"PORT": "0"}, wantErr: "PORT"},
		{name: "port trop grand", vars: map[string]string{"PORT": "70000"}, wantErr: "PORT"},
		{name: "mode inconnu", vars: map[string]string{"CANARY_MODE": "batch"}, wantErr: "CANARY_MODE"},
		{name: "mode cron", vars: map[string]string{"CANARY_MODE": "cron"},
			check: func(t *testing.T, c Config) {
				if c.Mode != "cron" {
					t.Errorf("mode = %q", c.Mode)
				}
			}},
		{name: "délai de démarrage", vars: map[string]string{"STARTUP_DELAY": "20"},
			check: func(t *testing.T, c Config) {
				if c.StartupDelay != 20*time.Second {
					t.Errorf("StartupDelay = %v", c.StartupDelay)
				}
			}},
		{name: "délai négatif", vars: map[string]string{"STARTUP_DELAY": "-1"}, wantErr: "STARTUP_DELAY"},
		{name: "code de sortie", vars: map[string]string{"CRON_EXIT_CODE": "3"},
			check: func(t *testing.T, c Config) {
				if c.CronExitCode != 3 {
					t.Errorf("CronExitCode = %d", c.CronExitCode)
				}
			}},
		{name: "code de sortie invalide", vars: map[string]string{"CRON_EXIT_CODE": "x"}, wantErr: "CRON_EXIT_CODE"},
		{name: "jeton et URL de rapport",
			vars: map[string]string{"CANARY_TOKEN": "s", "CANARY_REPORT_URL": "http://canary:80"},
			check: func(t *testing.T, c Config) {
				if c.Token != "s" || c.ReportURL != "http://canary:80" {
					t.Errorf("Token = %q, ReportURL = %q", c.Token, c.ReportURL)
				}
			}},
		{name: "DATABASE_URL invalide", vars: map[string]string{"DATABASE_URL": "redis://u:secretpw@h/0"},
			wantErr: "DATABASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(envOf(tt.vars))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("attendu une erreur contenant %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("erreur %q ne contient pas %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "secretpw") {
					t.Errorf("l'erreur divulgue le mot de passe : %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			tt.check(t, c)
		})
	}
}

func TestParseDatabaseURL(t *testing.T) {
	tests := []struct {
		raw     string
		want    DBTarget // Query vérifiée à part
		wantErr bool
	}{
		{raw: "postgresql://postgres:pw@canary-db-postgresql:5432/app",
			want: DBTarget{Engine: EnginePostgres, Host: "canary-db-postgresql", Port: 5432, User: "postgres", Password: "pw", Database: "app"}},
		{raw: "postgres://u:p@h/app",
			want: DBTarget{Engine: EnginePostgres, Host: "h", Port: 5432, User: "u", Password: "p", Database: "app"}},
		{raw: "mysql://mariadb:pw@canary-db-mariadb:3306/app",
			want: DBTarget{Engine: EngineMariaDB, Host: "canary-db-mariadb", Port: 3306, User: "mariadb", Password: "pw", Database: "app"}},
		{raw: "mariadb://u:p@h/app",
			want: DBTarget{Engine: EngineMariaDB, Host: "h", Port: 3306, User: "u", Password: "p", Database: "app"}},
		{raw: "mongodb://mongodb:pw@h:27017/app?authSource=admin",
			want: DBTarget{Engine: EngineMongo, Host: "h", Port: 27017, User: "mongodb", Password: "pw", Database: "app"}},
		{raw: "mongodb://u:p@h/app",
			want: DBTarget{Engine: EngineMongo, Host: "h", Port: 27017, User: "u", Password: "p", Database: "app"}},
		// Copier-coller depuis l'intranet : espaces et retour à la ligne autour.
		{raw: "  postgresql://u:p@h:5432/app\n",
			want: DBTarget{Engine: EnginePostgres, Host: "h", Port: 5432, User: "u", Password: "p", Database: "app"}},
		{raw: "postgresql://u:p%40ss@h/app",
			want: DBTarget{Engine: EnginePostgres, Host: "h", Port: 5432, User: "u", Password: "p@ss", Database: "app"}},
		{raw: "redis://h/0", wantErr: true},
		{raw: "postgresql:///app", wantErr: true},
		{raw: "postgresql://u:p@h", wantErr: true},
		{raw: "postgresql://u:p@h:notaport/app", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseDatabaseURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("attendu une erreur, obtenu %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			g := *got
			g.Query = nil
			if !reflect.DeepEqual(g, tt.want) {
				t.Errorf("obtenu %+v\nattendu %+v", g, tt.want)
			}
		})
	}

	got, err := ParseDatabaseURL("mongodb://mongodb:pw@h:27017/app?authSource=admin")
	if err != nil {
		t.Fatal(err)
	}
	if got.Query.Get("authSource") != "admin" {
		t.Errorf("authSource = %q, attendu admin", got.Query.Get("authSource"))
	}
}

func TestDBTargetURLRoundTrip(t *testing.T) {
	for _, raw := range []string{
		"postgresql://postgres:pw@h:5432/app",
		"mysql://mariadb:pw@h:3306/app",
		"mongodb://mongodb:pw@h:27017/app?authSource=admin",
	} {
		t.Run(raw, func(t *testing.T) {
			orig, err := ParseDatabaseURL(raw)
			if err != nil {
				t.Fatal(err)
			}
			back, err := ParseDatabaseURL(orig.WithCredentials("lecteur", "a@b:c/d").URL())
			if err != nil {
				t.Fatalf("URL() illisible : %v", err)
			}
			if back.User != "lecteur" || back.Password != "a@b:c/d" {
				t.Errorf("identifiants = %q/%q", back.User, back.Password)
			}
			if back.Engine != orig.Engine || back.Host != orig.Host || back.Port != orig.Port ||
				back.Database != orig.Database || back.Query.Encode() != orig.Query.Encode() {
				t.Errorf("cible modifiée : %+v devenu %+v", orig, back)
			}
		})
	}
}
