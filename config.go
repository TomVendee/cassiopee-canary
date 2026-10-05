package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Moteurs de base de données proposés par Cassiopée. Les valeurs sont celles
// que SiOps emploie dans ses manifestes.
const (
	EnginePostgres = "postgresql"
	EngineMariaDB  = "mariadb"
	EngineMongo    = "mongodb"
)

// allowedPorts sont les seuls ports que la NetworkPolicy de SiOps laisse
// atteindre depuis l'ingress controller. Sur un autre port, l'app démarre mais
// l'endpoint ne la joindra jamais.
var allowedPorts = map[int]bool{80: true, 8080: true, 443: true, 8443: true}

// defaultPorts donne le port de chaque moteur quand l'URL n'en précise pas.
var defaultPorts = map[string]int{EnginePostgres: 5432, EngineMariaDB: 3306, EngineMongo: 27017}

// engineByScheme associe chaque schéma d'URL accepté à son moteur. SiOps écrit
// `mysql://` pour MariaDB ; les alias sont acceptés pour la saisie à la main.
var engineByScheme = map[string]string{
	"postgresql": EnginePostgres,
	"postgres":   EnginePostgres,
	"mysql":      EngineMariaDB,
	"mariadb":    EngineMariaDB,
	"mongodb":    EngineMongo,
}

// canonicalScheme est le schéma que URL() réécrit pour chaque moteur.
var canonicalScheme = map[string]string{EnginePostgres: "postgresql", EngineMariaDB: "mysql", EngineMongo: "mongodb"}

// Config rassemble tout ce que l'app lit dans son environnement au démarrage.
type Config struct {
	Port         int
	PortAllowed  bool // 80, 8080, 443 ou 8443
	Mode         string
	StartupDelay time.Duration
	DB           *DBTarget // nil si DATABASE_URL est vide
	ReportURL    string
	CronExitCode int
	Token        string
}

// DBTarget décrit la base à joindre, telle que Cassiopée la donne dans son
// `connection_string`.
type DBTarget struct {
	Engine   string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Query    url.Values // conservée telle quelle (authSource=admin pour MongoDB)
}

// Load lit la configuration. getenv est os.Getenv en production ; les tests
// passent une fonction qui lit une table, ce qui évite de toucher au vrai
// environnement du processus.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Port:      8080,
		Mode:      "web",
		ReportURL: strings.TrimSpace(getenv("CANARY_REPORT_URL")),
		Token:     getenv("CANARY_TOKEN"),
	}

	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return Config{}, fmt.Errorf("PORT : %q n'est pas un port valide (1 à 65535)", v)
		}
		c.Port = p
	}
	c.PortAllowed = allowedPorts[c.Port]

	if v := getenv("CANARY_MODE"); v != "" {
		if v != "web" && v != "cron" {
			return Config{}, fmt.Errorf("CANARY_MODE : %q inconnu, valeurs possibles : web, cron", v)
		}
		c.Mode = v
	}

	if v := getenv("STARTUP_DELAY"); v != "" {
		s, err := strconv.Atoi(v)
		if err != nil || s < 0 {
			return Config{}, fmt.Errorf("STARTUP_DELAY : %q n'est pas un nombre de secondes positif", v)
		}
		c.StartupDelay = time.Duration(s) * time.Second
	}

	if v := getenv("CRON_EXIT_CODE"); v != "" {
		code, err := strconv.Atoi(v)
		if err != nil || code < 0 || code > 255 {
			return Config{}, fmt.Errorf("CRON_EXIT_CODE : %q n'est pas un code de sortie (0 à 255)", v)
		}
		c.CronExitCode = code
	}

	if v := strings.TrimSpace(getenv("DATABASE_URL")); v != "" {
		t, err := ParseDatabaseURL(v)
		if err != nil {
			return Config{}, fmt.Errorf("DATABASE_URL : %w", err)
		}
		c.DB = t
	}

	return c, nil
}

// ParseDatabaseURL lit un `connection_string` de Cassiopée. Les messages
// d'erreur ne reprennent jamais l'URL, qui contient le mot de passe.
func ParseDatabaseURL(raw string) (*DBTarget, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("URL illisible")
	}
	engine, ok := engineByScheme[u.Scheme]
	if !ok {
		return nil, fmt.Errorf("schéma %q non pris en charge (postgresql, mysql, mongodb)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("hôte manquant")
	}
	t := &DBTarget{
		Engine:   engine,
		Host:     u.Hostname(),
		Port:     defaultPorts[engine],
		Database: strings.TrimPrefix(u.Path, "/"),
		Query:    u.Query(),
	}
	if t.Database == "" {
		return nil, errors.New("nom de base manquant après l'hôte")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("port invalide")
		}
		t.Port = n
	}
	if u.User != nil {
		t.User = u.User.Username()
		t.Password, _ = u.User.Password()
	}
	return t, nil
}

// WithCredentials renvoie la même cible avec d'autres identifiants : c'est ce
// qui permet au formulaire de tester un utilisateur secondaire sans jamais
// changer d'hôte.
func (t DBTarget) WithCredentials(user, password string) DBTarget {
	t.User = user
	t.Password = password
	return t
}

// URL reconstruit l'URL du moteur, identifiants échappés. Les pilotes
// PostgreSQL et MongoDB la prennent telle quelle.
func (t DBTarget) URL() string {
	u := url.URL{
		Scheme:   canonicalScheme[t.Engine],
		User:     url.UserPassword(t.User, t.Password),
		Host:     net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		Path:     "/" + t.Database,
		RawQuery: t.Query.Encode(),
	}
	return u.String()
}
