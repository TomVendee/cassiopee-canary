package main

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

var now0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// baseInput est une entrée où tout est vert ; chaque cas en modifie un point.
func baseInput() CheckInput {
	h := http.Header{}
	h.Set("X-Forwarded-For", "163.5.1.1")
	h.Set("X-Forwarded-Proto", "https")
	probe := ProbeState{Hits: 5, LastHit: now0.Add(-5 * time.Second), IntervalSeconds: 10}
	return CheckInput{
		Runtime: Runtime{UID: 1000, CPULimited: true, MemoryLimited: true},
		Probes:  map[ProbeKind]ProbeState{ProbeStartup: {Hits: 1}, ProbeReady: probe, ProbeLive: probe},
		Headers: h,
		DB:      &DBStatus{Reachable: true, WriteOK: true, Since: now0.Add(-time.Hour)},
		Egress:  &EgressResult{InternalDNS: StepResult{OK: true}, ExternalDNS: StepResult{OK: true}},
		Now:     now0,
	}
}

func levelOf(t *testing.T, checks []Check, id int) (Level, string) {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c.Level, c.Star
		}
	}
	t.Fatalf("vérification %d absente", id)
	return "", ""
}

func TestEvaluateIDsAndAllGreen(t *testing.T) {
	checks := Evaluate(baseInput())
	var ids []int
	for _, c := range checks {
		ids = append(ids, c.ID)
		if c.Level != LevelOK {
			t.Errorf("vérification %d (%s) = %s, attendu ok", c.ID, c.Name, c.Level)
		}
		if c.Name == "" || c.Detail == "" {
			t.Errorf("vérification %d sans nom ou sans détail : %+v", c.ID, c)
		}
	}
	if want := []int{2, 3, 4, 5, 6, 7, 8, 10, 11}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ID = %v, attendu %v", ids, want)
	}
}

func TestEvaluateRules(t *testing.T) {
	tests := []struct {
		name   string
		id     int
		change func(*CheckInput)
		want   Level
		star   string
	}{
		{"root", 2, func(in *CheckInput) { in.Runtime.UID = 0 }, LevelKO, "application"},
		{"cpu illimité", 3, func(in *CheckInput) { in.Runtime.CPULimited = false }, LevelWarn, "application"},
		{"mémoire illimitée", 3, func(in *CheckInput) { in.Runtime.MemoryLimited = false }, LevelWarn, "application"},
		{"startup jamais appelée", 4, func(in *CheckInput) { in.Probes[ProbeStartup] = ProbeState{} }, LevelNA, "sondes"},
		{"ready jamais appelée", 5, func(in *CheckInput) { in.Probes[ProbeReady] = ProbeState{} }, LevelNA, "sondes"},
		{"ready il y a 25 s", 5, func(in *CheckInput) {
			in.Probes[ProbeReady] = ProbeState{Hits: 3, LastHit: now0.Add(-25 * time.Second), IntervalSeconds: 10}
		}, LevelOK, "sondes"},
		{"ready il y a 31 s", 5, func(in *CheckInput) {
			in.Probes[ProbeReady] = ProbeState{Hits: 3, LastHit: now0.Add(-31 * time.Second), IntervalSeconds: 10}
		}, LevelWarn, "sondes"},
		{"ready un seul appel", 5, func(in *CheckInput) {
			in.Probes[ProbeReady] = ProbeState{Hits: 1, LastHit: now0.Add(-time.Hour)}
		}, LevelOK, "sondes"},
		{"live plus appelée", 6, func(in *CheckInput) {
			in.Probes[ProbeLive] = ProbeState{Hits: 3, LastHit: now0.Add(-31 * time.Second), IntervalSeconds: 10}
		}, LevelWarn, "sondes"},
		{"accès direct", 7, func(in *CheckInput) { in.Headers.Del("X-Forwarded-For") }, LevelNA, "endpoint"},
		{"https sans ingress", 8, func(in *CheckInput) { in.Headers.Del("X-Forwarded-For") }, LevelNA, "endpoint"},
		{"http", 8, func(in *CheckInput) { in.Headers.Set("X-Forwarded-Proto", "http") }, LevelWarn, "endpoint"},
		{"pas de base", 10, func(in *CheckInput) { in.DB = nil }, LevelNA, "base"},
		{"auth refusée", 10, func(in *CheckInput) { in.DB = &DBStatus{AuthFailed: true, Since: now0} }, LevelKO, "base"},
		{"écriture refusée", 10, func(in *CheckInput) { in.DB = &DBStatus{Reachable: true, WriteOK: false} }, LevelWarn, "base"},
		{"injoignable 59 s", 10, func(in *CheckInput) { in.DB = &DBStatus{Since: now0.Add(-59 * time.Second)} }, LevelWarn, "base"},
		{"injoignable 60 s", 10, func(in *CheckInput) { in.DB = &DBStatus{Since: now0.Add(-60 * time.Second)} }, LevelKO, "base"},
		{"réseau pas mesuré", 11, func(in *CheckInput) { in.Egress = nil }, LevelNA, "reseau"},
		{"DNS externe KO", 11, func(in *CheckInput) { in.Egress.ExternalDNS = StepResult{Error: "x"} }, LevelKO, "reseau"},
		{"DNS interne KO", 11, func(in *CheckInput) { in.Egress.InternalDNS = StepResult{Error: "x"} }, LevelKO, "reseau"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInput()
			tt.change(&in)
			level, star := levelOf(t, Evaluate(in), tt.id)
			if level != tt.want || star != tt.star {
				t.Errorf("vérification %d = %s (%s), attendu %s (%s)", tt.id, level, star, tt.want, tt.star)
			}
		})
	}
}
