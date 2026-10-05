// Commande canary : application témoin à déployer avec Cassiopée. Voir
// README.md pour l'usage et docs/ pour le détail.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// version est remplacée à la compilation par -ldflags "-X main.version=…"
// (voir Dockerfile). Elle s'affiche sur la page : on voit ainsi quelle image
// tourne réellement après un changement de tag dans l'intranet.
var version = "dev"

const (
	cgroupDir     = "/sys/fs/cgroup"
	namespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration invalide :", err)
		os.Exit(2)
	}
	pod, _ := os.Hostname()

	// ctx est annulé à la réception de SIGTERM, ce que Kubernetes envoie avant
	// d'arrêter un pod (redéploiement, réduction du nombre de répliques).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if cfg.Mode == "cron" {
		os.Exit(RunCron(cfg, pod, &http.Client{Timeout: 10 * time.Second}, time.Now))
	}
	os.Exit(runWeb(ctx, cfg, pod))
}

// egressCheck prépare la mesure du réseau sortant : DNS de la base (sinon de
// l'API Kubernetes), DNS public et HTTPS vers example.com, en 3 s au plus.
func egressCheck(cfg Config) func(context.Context) EgressResult {
	internal := "kubernetes.default.svc"
	if cfg.DB != nil {
		internal = cfg.DB.Host
	}
	client := &http.Client{Timeout: 3 * time.Second}
	return func(ctx context.Context) EgressResult {
		return CheckEgress(ctx, net.DefaultResolver, client, internal, "https://example.com")
	}
}

// runWeb démarre le serveur et le garde jusqu'à l'annulation de ctx, puis
// l'arrête proprement en laissant 10 s aux requêtes en cours.
func runWeb(ctx context.Context, cfg Config, pod string) int {
	d := Deps{
		Cfg:     cfg,
		Version: version,
		Pod:     pod,
		Started: time.Now(),
		Now:     time.Now,
		Probes:  NewProbes(cfg.StartupDelay, time.Now),
		Cron:    NewCronLog(time.Now),
		Egress:  NewEgressWatcher(egressCheck(cfg)),
		Runtime: func() Runtime { return ReadRuntime(pod, cgroupDir, namespaceFile) },
		Environ: os.Environ,
	}
	go d.Egress.Run(ctx, 30*time.Second)

	if cfg.DB != nil {
		store, err := Open(*cfg.DB)
		if err != nil {
			slog.Error("base inutilisable, l'app démarre sans", "erreur", err)
		} else {
			defer store.Close()
			d.DB = NewMonitor(store, *cfg.DB, pod, time.Now)
			go d.DB.Run(ctx, DBPeriod)
		}
	}

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           NewServer(d),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	engine := "aucune"
	if cfg.DB != nil {
		engine = cfg.DB.Engine
	}
	slog.Info("démarrage", "version", version, "pod", pod, "port", cfg.Port, "mode", cfg.Mode, "base", engine)
	if !cfg.PortAllowed {
		slog.Warn("port hors NetworkPolicy : l'ingress ne joindra pas l'app (ports permis : 80, 8080, 443, 8443)", "port", cfg.Port)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serveur arrêté", "erreur", err)
			return 1
		}
	case <-ctx.Done():
	}

	slog.Info("arrêt demandé, fin des requêtes en cours")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("arrêt incomplet", "erreur", err)
		return 1
	}
	return 0
}
