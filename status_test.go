package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestStatusRoute(t *testing.T) {
	d := testDeps(Config{})
	d.Environ = func() []string { return []string{"DATABASE_URL=postgresql://u:pw@h:5432/app", "HELLO=world"} }
	h := NewServer(d)

	w := do(h, "GET", "/canary/api/status", nil, func(r *http.Request) {
		r.Host = "canary.cassiopee.sigl.epita.fr"
		r.Header.Set("X-Forwarded-For", "163.5.1.1")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Authorization", "Bearer x")
	})
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET /api/status = %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	if strings.Contains(body, ":pw@") {
		t.Error("le mot de passe de DATABASE_URL apparaît dans la réponse")
	}

	var s map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s["pod"] != d.Pod || s["version"] != "test" {
		t.Errorf("pod %v, version %v", s["pod"], s["version"])
	}
	if s["requestPath"] != "/canary/api/status" {
		t.Errorf("requestPath = %v, attendu le chemin reçu avant retrait du préfixe", s["requestPath"])
	}
	env := s["env"].(map[string]any)
	if !strings.Contains(env["DATABASE_URL"].(string), Masked) || env["HELLO"] != "world" {
		t.Errorf("env = %v", env)
	}
	headers := s["headers"].(map[string]any)
	if headers["Authorization"] != Masked || headers["Host"] != "canary.cassiopee.sigl.epita.fr" {
		t.Errorf("headers = %v", headers)
	}
	if s["db"] != nil {
		t.Errorf("db = %v, attendu null sans base", s["db"])
	}
	if cron, ok := s["cron"].([]any); !ok || len(cron) != 0 {
		t.Errorf("cron = %#v, attendu []", s["cron"])
	}
	levels := map[float64]string{}
	for _, c := range s["checks"].([]any) {
		m := c.(map[string]any)
		levels[m["id"].(float64)] = m["level"].(string)
	}
	if levels[7] != "ok" || levels[8] != "ok" {
		t.Errorf("vérifications 7 et 8 = %q, %q ; attendues ok", levels[7], levels[8])
	}
	for _, key := range []string{"startedAt", "uptimeSeconds", "port", "portAllowed", "runtime", "probes", "egress"} {
		if _, ok := s[key]; !ok {
			t.Errorf("champ %q absent", key)
		}
	}
}
