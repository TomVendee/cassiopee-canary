package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCPUMax(t *testing.T) {
	tests := []struct {
		in      string
		milli   int
		limited bool
		wantErr bool
	}{
		{in: "20000 100000\n", milli: 200, limited: true},
		{in: "200000 100000", milli: 2000, limited: true},
		{in: "max 100000", milli: 0, limited: false},
		{in: "x", wantErr: true},
		{in: "20000 0", wantErr: true},
	}
	for _, tt := range tests {
		milli, limited, err := ParseCPUMax(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseCPUMax(%q) : erreur attendue", tt.in)
			}
			continue
		}
		if err != nil || milli != tt.milli || limited != tt.limited {
			t.Errorf("ParseCPUMax(%q) = %d, %v, %v ; attendu %d, %v", tt.in, milli, limited, err, tt.milli, tt.limited)
		}
	}
}

func TestParseMemoryMax(t *testing.T) {
	tests := []struct {
		in      string
		bytes   int64
		limited bool
		wantErr bool
	}{
		{in: "268435456\n", bytes: 268435456, limited: true},
		{in: "max", bytes: 0, limited: false},
		{in: "-1", wantErr: true},
		{in: "abc", wantErr: true},
	}
	for _, tt := range tests {
		b, limited, err := ParseMemoryMax(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMemoryMax(%q) : erreur attendue", tt.in)
			}
			continue
		}
		if err != nil || b != tt.bytes || limited != tt.limited {
			t.Errorf("ParseMemoryMax(%q) = %d, %v, %v", tt.in, b, limited, err)
		}
	}
}

func TestGuessDeployment(t *testing.T) {
	tests := map[string]string{
		"canary-7f9c8d6b5-x2k9p":     "canary",
		"my-app-7f9c8d6b5-x2k9p":     "my-app",
		"canary":                     "",
		"canary-x2k9p":               "",
		"canary-cron-29318472-abcde": "canary-cron",
	}
	for pod, want := range tests {
		if got := GuessDeployment(pod); got != want {
			t.Errorf("GuessDeployment(%q) = %q, attendu %q", pod, got, want)
		}
	}
}

func TestReadRuntime(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cpu.max", "20000 100000\n")
	write("memory.max", "268435456\n")
	write("memory.current", "12345678\n")
	write("namespace", "cassiopee-app-jdoe")

	r := ReadRuntime("canary-7f9c8d6b5-x2k9p", dir, filepath.Join(dir, "namespace"))
	if r.Pod != "canary-7f9c8d6b5-x2k9p" || r.Deployment != "canary" || r.Namespace != "cassiopee-app-jdoe" {
		t.Errorf("identité = %q / %q / %q", r.Pod, r.Deployment, r.Namespace)
	}
	if !r.CPULimited || r.CPUMillicores != 200 {
		t.Errorf("CPU = %d (limité %v)", r.CPUMillicores, r.CPULimited)
	}
	if !r.MemoryLimited || r.MemoryMax != 268435456 || r.MemoryCurrent != 12345678 {
		t.Errorf("mémoire = %d / %d (limitée %v)", r.MemoryCurrent, r.MemoryMax, r.MemoryLimited)
	}
	if r.UID != os.Getuid() || r.GID != os.Getgid() {
		t.Errorf("uid/gid = %d/%d", r.UID, r.GID)
	}

	empty := ReadRuntime("p", t.TempDir(), "/nonexistent/namespace")
	if empty.CPULimited || empty.MemoryLimited || empty.Namespace != "" {
		t.Errorf("dossier vide : %+v", empty)
	}
}

func TestCanWrite(t *testing.T) {
	if !canWrite(t.TempDir()) {
		t.Error("canWrite(TempDir) = false")
	}
	if canWrite("/nonexistent") {
		t.Error("canWrite(/nonexistent) = true")
	}
}
