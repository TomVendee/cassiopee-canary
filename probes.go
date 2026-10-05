package main

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ProbeKind identifie une des trois sondes Kubernetes.
type ProbeKind string

const (
	ProbeStartup ProbeKind = "startup"
	ProbeReady   ProbeKind = "ready"
	ProbeLive    ProbeKind = "live"
)

// ProbeFailDuration est la durée d'un échec forcé. Il s'annule seul : avec une
// seule réplique, une readiness en échec retire le pod du service et rend la
// page injoignable, donc personne ne pourrait le rétablir à la main.
const ProbeFailDuration = 60 * time.Second

// probeHistory est le nombre d'appels kubelet gardés pour mesurer l'intervalle.
const probeHistory = 10

// ErrNotFailable est renvoyée si l'on tente de faire échouer la sonde startup.
var ErrNotFailable = errors.New("seules ready et live peuvent échouer")

// ProbeState est ce que la page affiche pour une sonde.
type ProbeState struct {
	Hits            int       `json:"hits"` // appels de kubelet seulement
	LastHit         time.Time `json:"lastHit"`
	IntervalSeconds float64   `json:"intervalSeconds"` // écart moyen entre les derniers appels kubelet
	FailingUntil    time.Time `json:"failingUntil"`
	Healthy         bool      `json:"healthy"` // ce que la sonde répondrait maintenant
}

type probe struct {
	hits         int
	recent       []time.Time // derniers appels kubelet, le plus ancien en tête
	failingUntil time.Time
}

// Probes tient l'état des trois sondes. Kubelet et la page y accèdent en même
// temps depuis des goroutines différentes : un mutex protège tout.
type Probes struct {
	mu        sync.Mutex
	now       func() time.Time
	readyFrom time.Time // la sonde startup réussit à partir de cet instant
	probes    map[ProbeKind]*probe
}

// IsKubelet reconnaît un appel de kubelet à son User-Agent.
func IsKubelet(userAgent string) bool {
	return strings.HasPrefix(userAgent, "kube-probe/")
}

// NewProbes crée les sondes. La sonde startup répond 503 pendant startupDelay.
func NewProbes(startupDelay time.Duration, now func() time.Time) *Probes {
	return &Probes{
		now:       now,
		readyFrom: now().Add(startupDelay),
		probes: map[ProbeKind]*probe{
			ProbeStartup: {}, ProbeReady: {}, ProbeLive: {},
		},
	}
}

// Hit enregistre un appel et dit ce qu'il faut répondre : true pour 200,
// false pour 503. Seuls les appels de kubelet sont comptés.
func (p *Probes) Hit(kind ProbeKind, fromKubelet bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.probes[kind]
	if !ok {
		return false
	}
	now := p.now()
	if fromKubelet {
		pr.hits++
		pr.recent = append(pr.recent, now)
		if len(pr.recent) > probeHistory {
			pr.recent = pr.recent[1:]
		}
	}
	return p.healthy(kind, pr, now)
}

// Fail fait échouer ready ou live pendant ProbeFailDuration.
func (p *Probes) Fail(kind ProbeKind) error {
	if kind != ProbeReady && kind != ProbeLive {
		return ErrNotFailable
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probes[kind].failingUntil = p.now().Add(ProbeFailDuration)
	return nil
}

// Snapshot renvoie une copie de l'état des sondes, sûre à lire sans verrou.
func (p *Probes) Snapshot() map[ProbeKind]ProbeState {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make(map[ProbeKind]ProbeState, len(p.probes))
	for kind, pr := range p.probes {
		s := ProbeState{Hits: pr.hits, Healthy: p.healthy(kind, pr, now)}
		if n := len(pr.recent); n > 0 {
			s.LastHit = pr.recent[n-1]
		}
		if n := len(pr.recent); n > 1 {
			s.IntervalSeconds = pr.recent[n-1].Sub(pr.recent[0]).Seconds() / float64(n-1)
		}
		if now.Before(pr.failingUntil) {
			s.FailingUntil = pr.failingUntil
		}
		out[kind] = s
	}
	return out
}

// healthy suppose le verrou déjà pris.
func (p *Probes) healthy(kind ProbeKind, pr *probe, now time.Time) bool {
	if kind == ProbeStartup {
		return !now.Before(p.readyFrom)
	}
	return !now.Before(pr.failingUntil)
}
