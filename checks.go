package main

import (
	"fmt"
	"net/http"
	"time"
)

// Level est le voyant d'une vérification.
type Level string

const (
	LevelOK   Level = "ok"   // vert
	LevelWarn Level = "warn" // orange : dégradé
	LevelKO   Level = "ko"   // rouge
	LevelNA   Level = "na"   // gris : non configuré, exclu du compteur
)

// dbWarnWindow : une base injoignable reste orange pendant ce délai (une
// suspension ou un redémarrage en cours), puis passe au rouge.
const dbWarnWindow = 60 * time.Second

// Check est une vérification affichée sur la page. Star nomme l'étoile de la
// constellation à laquelle elle appartient.
type Check struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Star   string `json:"star"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
}

// CheckInput rassemble ce que le serveur sait au moment de la requête.
type CheckInput struct {
	Runtime Runtime
	Probes  map[ProbeKind]ProbeState
	Headers http.Header
	DB      *DBStatus     // nil si pas de base
	Egress  *EgressResult // nil si pas encore mesuré
	Now     time.Time
}

// Evaluate calcule les vérifications que le serveur peut constater : 2 à 8,
// 10 et 11 (spec §7). La 1 (serveur joignable) et la 9 (WebSocket) ne se
// voient que depuis le navigateur ; la page les ajoute.
func Evaluate(in CheckInput) []Check {
	return []Check{
		checkNonRoot(in.Runtime),
		checkLimits(in.Runtime),
		checkStartup(in.Probes[ProbeStartup]),
		checkProbe(5, "Sonde readiness", in.Probes[ProbeReady], in.Now),
		checkProbe(6, "Sonde liveness", in.Probes[ProbeLive], in.Now),
		checkIngress(in.Headers),
		checkHTTPS(in.Headers),
		checkDB(in.DB, in.Now),
		checkDNS(in.Egress),
	}
}

func checkNonRoot(r Runtime) Check {
	c := Check{ID: 2, Name: "Non-root", Star: "application", Level: LevelOK,
		Detail: fmt.Sprintf("uid %d, gid %d", r.UID, r.GID)}
	if r.UID == 0 {
		c.Level, c.Detail = LevelKO, "le conteneur tourne en root (uid 0)"
	}
	return c
}

func checkLimits(r Runtime) Check {
	c := Check{ID: 3, Name: "Limites appliquées", Star: "application", Level: LevelOK}
	cpu, mem := "CPU illimité", "mémoire illimitée"
	if r.CPULimited {
		cpu = fmt.Sprintf("CPU %dm", r.CPUMillicores)
	}
	if r.MemoryLimited {
		mem = fmt.Sprintf("mémoire %d Mio", r.MemoryMax>>20)
	}
	c.Detail = cpu + ", " + mem
	if !r.CPULimited || !r.MemoryLimited {
		c.Level = LevelWarn
	}
	return c
}

func checkStartup(p ProbeState) Check {
	c := Check{ID: 4, Name: "Sonde startup", Star: "sondes", Level: LevelNA, Detail: "jamais appelée par kubelet"}
	if p.Hits > 0 {
		c.Level, c.Detail = LevelOK, fmt.Sprintf("%d appels de kubelet", p.Hits)
	}
	return c
}

// checkProbe : une sonde est verte tant que kubelet l'appelle au rythme
// mesuré ; au-delà de trois intervalles sans appel, elle passe orange.
func checkProbe(id int, name string, p ProbeState, now time.Time) Check {
	c := Check{ID: id, Name: name, Star: "sondes", Level: LevelNA, Detail: "jamais appelée par kubelet"}
	if p.Hits == 0 {
		return c
	}
	since := now.Sub(p.LastHit)
	c.Level = LevelOK
	c.Detail = fmt.Sprintf("%d appels, dernier il y a %d s", p.Hits, int(since.Seconds()))
	if p.IntervalSeconds > 0 && since >= time.Duration(3*p.IntervalSeconds*float64(time.Second)) {
		c.Level = LevelWarn
		c.Detail += fmt.Sprintf(" (attendu toutes les %.0f s)", p.IntervalSeconds)
	}
	// Un échec forcé répond 503 à kubelet : la sonde ne peut pas rester verte.
	if p.FailingUntil.After(now) {
		c.Level = LevelWarn
		c.Detail = fmt.Sprintf("échec forcé, encore %d s", int(p.FailingUntil.Sub(now).Seconds()))
	}
	return c
}

func checkIngress(h http.Header) Check {
	c := Check{ID: 7, Name: "Passage par l'ingress", Star: "endpoint", Level: LevelNA,
		Detail: "accès direct, sans ingress"}
	if xff := h.Get("X-Forwarded-For"); xff != "" {
		c.Level, c.Detail = LevelOK, "X-Forwarded-For "+xff
	}
	return c
}

func checkHTTPS(h http.Header) Check {
	c := Check{ID: 8, Name: "HTTPS", Star: "endpoint", Level: LevelNA, Detail: "accès direct, sans ingress"}
	if h.Get("X-Forwarded-For") == "" {
		return c
	}
	proto := h.Get("X-Forwarded-Proto")
	if proto == "https" {
		c.Level, c.Detail = LevelOK, "TLS terminé par l'ingress"
	} else {
		c.Level, c.Detail = LevelWarn, fmt.Sprintf("requête arrivée en %q", proto)
	}
	return c
}

func checkDB(db *DBStatus, now time.Time) Check {
	c := Check{ID: 10, Name: "Base de données", Star: "base", Level: LevelNA, Detail: "DATABASE_URL vide"}
	switch {
	case db == nil:
	case db.AuthFailed:
		c.Level, c.Detail = LevelKO, "authentification refusée : "+db.LastError
	case db.Reachable && db.WriteOK:
		c.Level, c.Detail = LevelOK, fmt.Sprintf("%s, %d lignes", db.Engine, db.Rows)
	case db.Reachable:
		c.Level, c.Detail = LevelWarn, "joignable, écriture en échec : "+db.LastError
	default:
		down := now.Sub(db.Since)
		c.Level = LevelKO
		if down < dbWarnWindow {
			c.Level = LevelWarn
		}
		c.Detail = fmt.Sprintf("injoignable depuis %d s : %s", int(down.Seconds()), db.LastError)
	}
	return c
}

func checkDNS(e *EgressResult) Check {
	c := Check{ID: 11, Name: "DNS", Star: "reseau", Level: LevelNA, Detail: "pas encore mesuré"}
	if e == nil {
		return c
	}
	if e.InternalDNS.OK && e.ExternalDNS.OK {
		c.Level, c.Detail = LevelOK, "interne ("+e.InternalHost+") et externe résolus"
		return c
	}
	c.Level = LevelKO
	c.Detail = "échec : " + e.InternalDNS.Error + e.ExternalDNS.Error
	return c
}
