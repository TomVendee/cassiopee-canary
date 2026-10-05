package main

// Types provisoires, remplacés au fil des tâches suivantes (base, cron,
// réseau sortant). Ce fichier disparaît quand tous existent.

// Monitor surveille la base (tâche 5).
type Monitor struct{}

// CronLog garde les battements du mode cron (tâche 8).
type CronLog struct{}

// EgressWatcher mesure le réseau sortant (tâche 8).
type EgressWatcher struct{}
