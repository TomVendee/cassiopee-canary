package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// page est la page web, embarquée dans le binaire à la compilation par la
// directive //go:embed : l'image n'a besoin d'aucun fichier à côté.
//
//go:embed web/index.html
var page []byte

// maxUpload borne le test d'envoi : assez pour révéler une limite d'ingress
// (proxy-body-size), pas assez pour servir à saturer le pod.
const maxUpload = 50 << 20

// Deps regroupe tout ce dont le serveur a besoin. main les construit pour de
// vrai ; les tests passent des versions factices.
type Deps struct {
	Cfg     Config
	Version string
	Pod     string
	Started time.Time
	Now     func() time.Time
	Probes  *Probes
	DB      *Monitor       // nil si pas de base
	Cron    *CronLog       // battements reçus du mode cron
	Egress  *EgressWatcher // réseau sortant
	Runtime func() Runtime
	Environ func() []string
}

// probePaths sont exemptés d'authentification : kubelet n'a pas le jeton.
var probePaths = map[string]ProbeKind{
	"/startup": ProbeStartup,
	"/ready":   ProbeReady,
	"/healthz": ProbeLive,
}

// exactRoutes sont reconnues seulement en fin de chemin ; "/api/" préfixe
// toutes les autres.
var exactRoutes = []string{"/startup", "/ready", "/healthz", "/ws"}

// StripToKnownRoute retire tout ce qui précède une route connue :
// « /canary/api/status » devient « /api/status ». Un chemin sans route connue
// sert la page. Ainsi l'app marche derrière n'importe quel chemin d'endpoint,
// avec ou sans rewrite-target.
func StripToKnownRoute(path string) string {
	if i := strings.Index(path, "/api/"); i >= 0 {
		return path[i:]
	}
	for _, route := range exactRoutes {
		if strings.HasSuffix(path, route) {
			return route
		}
	}
	return "/"
}

// NewServer assemble les routes. L'ordre des couches compte : on retire
// d'abord le préfixe, puis on vérifie l'authentification sur le chemin nettoyé.
func NewServer(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", servePage)
	for path, kind := range probePaths {
		mux.HandleFunc("GET "+path, probeHandler(d.Probes, kind))
	}
	mux.HandleFunc("POST /api/probes/{kind}", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Probes.Fail(ProbeKind(r.PathValue("kind"))); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/upload", handleUpload)
	mux.HandleFunc("POST /api/db/write", func(w http.ResponseWriter, r *http.Request) {
		if d.DB == nil {
			http.Error(w, "base non configurée (DATABASE_URL vide)", http.StatusConflict)
			return
		}
		if err := d.DB.Write(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/db/credentials", func(w http.ResponseWriter, r *http.Request) {
		handleCredentials(w, r, d.DB)
	})
	mux.HandleFunc("POST /api/cron", func(w http.ResponseWriter, r *http.Request) {
		handleCron(w, r, d.Cron)
	})

	return stripPrefix(requireToken(d.Cfg.Token, mux))
}

// maxSmallBody borne les corps JSON courts (identifiants, battements).
const maxSmallBody = 1 << 10

// decodeSmallJSON lit un corps JSON d'au plus 1 Ko. En cas d'erreur, la
// réponse est déjà écrite (413 ou 400) et la fonction renvoie false.
func decodeSmallJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSmallBody)).Decode(v)
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, "corps trop gros", http.StatusRequestEntityTooLarge)
		return false
	case err != nil:
		http.Error(w, "JSON invalide", http.StatusBadRequest)
		return false
	}
	return true
}

// handleCredentials teste des identifiants fournis par le formulaire. Il ne
// prend que l'utilisateur et le mot de passe : l'hôte reste celui de
// DATABASE_URL, on ne peut donc pas s'en servir pour sonder le réseau.
func handleCredentials(w http.ResponseWriter, r *http.Request, m *Monitor) {
	if m == nil {
		http.Error(w, "base non configurée (DATABASE_URL vide)", http.StatusConflict)
		return
	}
	var in struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decodeSmallJSON(w, r, &in) {
		return
	}
	if in.User == "" || in.Password == "" {
		http.Error(w, "utilisateur et mot de passe requis", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, m.TryCredentials(r.Context(), in.User, in.Password))
}

func servePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(page)
}

func probeHandler(p *Probes, kind ProbeKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p.Hit(kind, IsKubelet(r.UserAgent())) {
			io.WriteString(w, "ok\n")
			return
		}
		http.Error(w, "échec", http.StatusServiceUnavailable)
	}
}

// handleUpload lit et jette le corps, puis renvoie la taille reçue.
func handleUpload(w http.ResponseWriter, r *http.Request) {
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxUpload))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, "corps trop gros", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"bytes": n})
}

func stripPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = StripToKnownRoute(r.URL.Path)
		r.URL.RawPath = ""
		next.ServeHTTP(w, r)
	})
}

// requireToken impose une authentification Basic quand CANARY_TOKEN est
// défini. Le navigateur affiche alors sa propre fenêtre de connexion :
// l'identifiant est libre, le mot de passe est le jeton.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, isProbe := probePaths[r.URL.Path]; isProbe {
			next.ServeHTTP(w, r)
			return
		}
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="canary"`)
			http.Error(w, "authentification requise", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSON envoie v en JSON avec le statut donné.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
