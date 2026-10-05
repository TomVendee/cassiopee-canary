package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testDeps construit des dépendances minimales ; chaque test ajuste ce qu'il
// lui faut.
func testDeps(cfg Config) Deps {
	return Deps{
		Cfg:     cfg,
		Version: "test",
		Pod:     "canary-7f9c8d6b5-x2k9p",
		Started: time.Now(),
		Now:     time.Now,
		Probes:  NewProbes(cfg.StartupDelay, time.Now),
		Cron:    NewCronLog(time.Now),
		Runtime: func() Runtime { return Runtime{Pod: "canary-7f9c8d6b5-x2k9p", UID: 1000} },
		Environ: func() []string { return nil },
	}
}

// do envoie une requête au serveur et renvoie la réponse enregistrée.
func do(h http.Handler, method, path string, body []byte, setup ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	for _, f := range setup {
		f(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStripToKnownRoute(t *testing.T) {
	tests := map[string]string{
		"/api/status":              "/api/status",
		"/canary/api/status":       "/api/status",
		"/a/b/healthz":             "/healthz",
		"/x/ws":                    "/ws",
		"/canary/":                 "/",
		"/canary":                  "/",
		"/":                        "/",
		"/healthzz":                "/",
		"/ready/extra":             "/",
		"/myapi/x":                 "/",
		"/canary/api/probes/ready": "/api/probes/ready",
	}
	for in, want := range tests {
		if got := StripToKnownRoute(in); got != want {
			t.Errorf("StripToKnownRoute(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestProbeRoutes(t *testing.T) {
	d := testDeps(Config{})
	h := NewServer(d)
	kubelet := func(r *http.Request) { r.Header.Set("User-Agent", "kube-probe/1.31") }
	if w := do(h, "GET", "/ready", nil, kubelet); w.Code != 200 {
		t.Errorf("GET /ready = %d", w.Code)
	}
	if n := d.Probes.Snapshot()[ProbeReady].Hits; n != 1 {
		t.Errorf("Hits ready = %d, attendu 1", n)
	}
	if w := do(h, "GET", "/canary/healthz", nil, kubelet); w.Code != 200 {
		t.Errorf("GET /canary/healthz = %d", w.Code)
	}
	if n := d.Probes.Snapshot()[ProbeLive].Hits; n != 1 {
		t.Errorf("Hits live = %d, attendu 1", n)
	}

	slow := NewServer(testDeps(Config{StartupDelay: 20 * time.Second}))
	if w := do(slow, "GET", "/startup", nil, kubelet); w.Code != 503 {
		t.Errorf("GET /startup pendant le délai = %d, attendu 503", w.Code)
	}
}

func TestFailRoute(t *testing.T) {
	h := NewServer(testDeps(Config{}))
	if w := do(h, "POST", "/canary/api/probes/ready", nil); w.Code != 204 {
		t.Errorf("POST /api/probes/ready = %d, attendu 204", w.Code)
	}
	if w := do(h, "GET", "/ready", nil); w.Code != 503 {
		t.Errorf("GET /ready après échec = %d, attendu 503", w.Code)
	}
	if w := do(h, "POST", "/api/probes/startup", nil); w.Code != 400 {
		t.Errorf("POST /api/probes/startup = %d, attendu 400", w.Code)
	}
	if w := do(h, "GET", "/api/probes/ready", nil); w.Code != 405 {
		t.Errorf("GET /api/probes/ready = %d, attendu 405", w.Code)
	}
}

func TestUpload(t *testing.T) {
	h := NewServer(testDeps(Config{}))
	w := do(h, "POST", "/api/upload", make([]byte, 1<<20))
	if w.Code != 200 {
		t.Fatalf("upload 1 Mo = %d", w.Code)
	}
	var got struct{ Bytes int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Bytes != 1<<20 {
		t.Errorf("réponse %q, attendu {\"bytes\":1048576}", w.Body.String())
	}
	if w := do(h, "POST", "/api/upload", make([]byte, 50<<20+1)); w.Code != 413 {
		t.Errorf("upload 50 Mo + 1 = %d, attendu 413", w.Code)
	}
}

func TestAuth(t *testing.T) {
	h := NewServer(testDeps(Config{Token: "s3cret"}))
	w := do(h, "GET", "/", nil)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Basic realm="canary"` {
		t.Errorf("sans auth : %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if w := do(h, "GET", "/", nil, func(r *http.Request) { r.SetBasicAuth("x", "s3cret") }); w.Code != 200 {
		t.Errorf("bon jeton : %d", w.Code)
	}
	if w := do(h, "GET", "/", nil, func(r *http.Request) { r.SetBasicAuth("x", "faux") }); w.Code != 401 {
		t.Errorf("mauvais jeton : %d", w.Code)
	}
	for _, p := range []string{"/healthz", "/ready", "/startup", "/canary/healthz"} {
		if w := do(h, "GET", p, nil); w.Code != 200 {
			t.Errorf("%s sans auth = %d, attendu 200 (sondes exemptées)", p, w.Code)
		}
	}
	open := NewServer(testDeps(Config{}))
	if w := do(open, "GET", "/", nil); w.Code != 200 {
		t.Errorf("sans jeton configuré : %d", w.Code)
	}
}

func TestPageServed(t *testing.T) {
	h := NewServer(testDeps(Config{}))
	w := do(h, "GET", "/canary/", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("GET /canary/ = %d, %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Cassiopée canary") {
		t.Error("la page ne contient pas son titre")
	}
}
