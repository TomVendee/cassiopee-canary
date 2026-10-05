package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CronLogSize est le nombre de battements gardés en mémoire.
const CronLogSize = 20

// Heartbeat est le signe de vie qu'envoie une exécution du CronJob.
type Heartbeat struct {
	Pod        string    `json:"pod"`
	ExitCode   int       `json:"exitCode"`
	SentAt     time.Time `json:"sentAt"`
	ReceivedAt time.Time `json:"receivedAt"`
}

// CronLog garde les derniers battements reçus par l'app web.
type CronLog struct {
	mu   sync.Mutex
	now  func() time.Time
	list []Heartbeat // le plus ancien en tête
}

// NewCronLog crée un journal vide.
func NewCronLog(now func() time.Time) *CronLog {
	return &CronLog{now: now}
}

// Add enregistre un battement, horodaté à sa réception.
func (l *CronLog) Add(h Heartbeat) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h.ReceivedAt = l.now()
	l.list = append(l.list, h)
	if len(l.list) > CronLogSize {
		l.list = l.list[len(l.list)-CronLogSize:]
	}
}

// List renvoie les battements, le plus récent d'abord. Jamais nil : la page
// reçoit [] et non null.
func (l *CronLog) List() []Heartbeat {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Heartbeat, len(l.list))
	for i, h := range l.list {
		out[len(l.list)-1-i] = h
	}
	return out
}

// RunCron est le mode CronJob : un battement envoyé à l'app web, puis sortie
// avec CRON_EXIT_CODE. Un échec d'envoi est journalisé sans changer le code de
// sortie, pour que restartPolicy ne réagisse qu'au code demandé.
func RunCron(cfg Config, pod string, client *http.Client, now func() time.Time) int {
	if cfg.ReportURL == "" {
		slog.Warn("CANARY_REPORT_URL vide : aucun battement envoyé", "code", cfg.CronExitCode)
		return cfg.CronExitCode
	}
	body, _ := json.Marshal(Heartbeat{Pod: pod, ExitCode: cfg.CronExitCode, SentAt: now()})
	url := strings.TrimSuffix(cfg.ReportURL, "/") + "/api/cron"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("CANARY_REPORT_URL invalide", "erreur", err)
		return cfg.CronExitCode
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.SetBasicAuth("cron", cfg.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("battement non envoyé", "url", url, "erreur", err)
		return cfg.CronExitCode
	}
	resp.Body.Close()
	slog.Info("battement envoyé", "url", url, "statut", resp.StatusCode, "code", cfg.CronExitCode)
	return cfg.CronExitCode
}

// handleCron reçoit un battement du mode cron.
func handleCron(w http.ResponseWriter, r *http.Request, l *CronLog) {
	var h Heartbeat
	if !decodeSmallJSON(w, r, &h) {
		return
	}
	l.Add(h)
	w.WriteHeader(http.StatusNoContent)
}
