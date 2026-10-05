package main

import (
	"context"
	"net/http"
	"time"
)

// Status est l'instantané que la page lit toutes les 2 s. Tout vient du même
// pod et de la même requête : avec plusieurs répliques, des appels séparés
// tomberaient sur des pods différents et mélangeraient leurs données.
type Status struct {
	Pod           string                   `json:"pod"`
	Version       string                   `json:"version"`
	StartedAt     time.Time                `json:"startedAt"`
	UptimeSeconds int64                    `json:"uptimeSeconds"`
	Port          int                      `json:"port"`
	PortAllowed   bool                     `json:"portAllowed"`
	RequestPath   string                   `json:"requestPath"` // chemin reçu, préfixe compris
	Env           map[string]string        `json:"env"`
	Runtime       Runtime                  `json:"runtime"`
	Headers       map[string]string        `json:"headers"`
	Probes        map[ProbeKind]ProbeState `json:"probes"`
	DB            *DBStatus                `json:"db"`
	Egress        *EgressResult            `json:"egress"`
	Cron          []Heartbeat              `json:"cron"`
	Checks        []Check                  `json:"checks"`
}

// originalPathKey range dans le contexte de la requête le chemin d'avant le
// retrait du préfixe.
type originalPathKey struct{}

func withOriginalPath(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), originalPathKey{}, r.URL.Path))
}

func originalPath(r *http.Request) string {
	p, _ := r.Context().Value(originalPathKey{}).(string)
	return p
}

// buildStatus assemble l'instantané pour une requête.
func buildStatus(d Deps, r *http.Request) Status {
	now := d.Now()
	s := Status{
		Pod:           d.Pod,
		Version:       d.Version,
		StartedAt:     d.Started,
		UptimeSeconds: int64(now.Sub(d.Started).Seconds()),
		Port:          d.Cfg.Port,
		PortAllowed:   d.Cfg.PortAllowed,
		RequestPath:   originalPath(r),
		Env:           MaskEnv(d.Environ()),
		Runtime:       d.Runtime(),
		Headers:       MaskHeaders(r.Header),
		Probes:        d.Probes.Snapshot(),
		Cron:          []Heartbeat{},
	}
	// Go retire Host des en-têtes pour le ranger dans r.Host : on le remet,
	// c'est lui qui montre le nom d'hôte de l'endpoint.
	s.Headers["Host"] = r.Host
	if d.DB != nil {
		db := d.DB.Status()
		s.DB = &db
	}
	if d.Egress != nil {
		if e, ok := d.Egress.Last(); ok {
			s.Egress = &e
		}
	}
	if d.Cron != nil {
		s.Cron = d.Cron.List()
	}
	s.Checks = Evaluate(CheckInput{
		Runtime: s.Runtime,
		Probes:  s.Probes,
		Headers: r.Header,
		DB:      s.DB,
		Egress:  s.Egress,
		Now:     now,
	})
	return s
}
