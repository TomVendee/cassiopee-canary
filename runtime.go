package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Runtime décrit le conteneur tel qu'il se voit de l'intérieur : identité,
// limites appliquées par Kubernetes, droits sur le système de fichiers.
type Runtime struct {
	Pod           string `json:"pod"`
	Namespace     string `json:"namespace"`  // vide hors Kubernetes
	Deployment    string `json:"deployment"` // deviné depuis le nom du pod
	UID           int    `json:"uid"`
	GID           int    `json:"gid"`
	CPUMillicores int    `json:"cpuMillicores"`
	CPULimited    bool   `json:"cpuLimited"`
	MemoryMax     int64  `json:"memoryMax"`
	MemoryCurrent int64  `json:"memoryCurrent"`
	MemoryLimited bool   `json:"memoryLimited"`
	RootWritable  bool   `json:"rootWritable"`
	TmpWritable   bool   `json:"tmpWritable"`
}

// ParseCPUMax lit le fichier cgroup v2 `cpu.max` (« quota période », ou
// « max période » sans limite) et le convertit en millicœurs, l'unité des
// limites Kubernetes : « 20000 100000 » vaut 200m.
func ParseCPUMax(s string) (millicores int, limited bool, err error) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, false, errors.New("cpu.max : format inattendu")
	}
	if fields[0] == "max" {
		return 0, false, nil
	}
	quota, err1 := strconv.Atoi(fields[0])
	period, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil || quota < 0 || period <= 0 {
		return 0, false, errors.New("cpu.max : valeurs invalides")
	}
	return quota * 1000 / period, true, nil
}

// ParseMemoryMax lit le fichier cgroup v2 `memory.max` : un nombre d'octets,
// ou « max » sans limite.
func ParseMemoryMax(s string) (bytes int64, limited bool, err error) {
	s = strings.TrimSpace(s)
	if s == "max" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false, errors.New("memory.max : valeur invalide")
	}
	return n, true, nil
}

// GuessDeployment devine le nom du déploiement à partir de celui du pod.
// Kubernetes nomme les pods « <déploiement>-<hash>-<suffixe> » : on retire les
// deux derniers segments. Renvoie "" si le nom ne suit pas ce motif.
func GuessDeployment(pod string) string {
	parts := strings.Split(pod, "-")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], "-")
}

// ReadRuntime rassemble l'état du conteneur. En production, cgroupDir vaut
// /sys/fs/cgroup et namespaceFile le fichier du compte de service monté par
// Kubernetes. Un fichier absent laisse simplement le champ à sa valeur nulle.
func ReadRuntime(pod, cgroupDir, namespaceFile string) Runtime {
	r := Runtime{
		Pod:          pod,
		Deployment:   GuessDeployment(pod),
		UID:          os.Getuid(),
		GID:          os.Getgid(),
		RootWritable: canWrite("/"),
		TmpWritable:  canWrite(os.TempDir()),
	}
	if b, err := os.ReadFile(namespaceFile); err == nil {
		r.Namespace = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(cgroupDir, "cpu.max")); err == nil {
		r.CPUMillicores, r.CPULimited, _ = ParseCPUMax(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(cgroupDir, "memory.max")); err == nil {
		r.MemoryMax, r.MemoryLimited, _ = ParseMemoryMax(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(cgroupDir, "memory.current")); err == nil {
		r.MemoryCurrent, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	return r
}

// canWrite essaie de créer puis de supprimer un fichier dans dir : c'est le
// seul moyen fiable de savoir si readOnlyRootFilesystem s'applique.
func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".canary-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
