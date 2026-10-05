package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunCronReports(t *testing.T) {
	var got Heartbeat
	var path, pass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		_, pass, _ = r.BasicAuth()
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := Config{ReportURL: srv.URL + "/", Token: "tok", CronExitCode: 3}
	if code := RunCron(cfg, "canary-cron-x", srv.Client(), time.Now); code != 3 {
		t.Errorf("RunCron = %d, attendu 3", code)
	}
	if path != "POST /api/cron" || pass != "tok" {
		t.Errorf("requête %q, mot de passe %q", path, pass)
	}
	if got.Pod != "canary-cron-x" || got.ExitCode != 3 || got.SentAt.IsZero() {
		t.Errorf("battement reçu : %+v", got)
	}
}

func TestRunCronWithoutURL(t *testing.T) {
	if code := RunCron(Config{CronExitCode: 4}, "p", http.DefaultClient, time.Now); code != 4 {
		t.Errorf("RunCron = %d, attendu 4", code)
	}
}

func TestRunCronServerDown(t *testing.T) {
	cfg := Config{ReportURL: "http://127.0.0.1:1", CronExitCode: 0}
	if code := RunCron(cfg, "p", &http.Client{Timeout: time.Second}, time.Now); code != 0 {
		t.Errorf("RunCron = %d, attendu 0 même si le rapport échoue", code)
	}
}

func TestCronLogKeeps20(t *testing.T) {
	l := NewCronLog(time.Now)
	for i := 0; i < 25; i++ {
		l.Add(Heartbeat{Pod: fmt.Sprintf("p%d", i)})
	}
	list := l.List()
	if len(list) != CronLogSize {
		t.Fatalf("%d battements gardés, attendu %d", len(list), CronLogSize)
	}
	if list[0].Pod != "p24" || list[19].Pod != "p5" {
		t.Errorf("ordre : premier %q, dernier %q", list[0].Pod, list[19].Pod)
	}
	if list[0].ReceivedAt.IsZero() {
		t.Error("ReceivedAt non renseigné")
	}
}

func TestCronRoute(t *testing.T) {
	d := testDeps(Config{})
	h := NewServer(d)
	if w := do(h, "POST", "/canary/api/cron", []byte(`{"pod":"canary-cron-x","exitCode":1}`)); w.Code != 204 {
		t.Fatalf("POST /api/cron = %d", w.Code)
	}
	if list := d.Cron.List(); len(list) != 1 || list[0].Pod != "canary-cron-x" || list[0].ExitCode != 1 {
		t.Errorf("battements : %+v", list)
	}
	if w := do(h, "POST", "/api/cron", []byte(`{`)); w.Code != 400 {
		t.Errorf("JSON invalide = %d, attendu 400", w.Code)
	}
	big := []byte(`{"pod":"` + strings.Repeat("x", 2048) + `"}`)
	if w := do(h, "POST", "/api/cron", big); w.Code != 413 {
		t.Errorf("corps de 2 Ko = %d, attendu 413", w.Code)
	}
}
