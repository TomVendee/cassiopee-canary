package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock est une horloge que le test avance à la main.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestStartupDelay(t *testing.T) {
	clock := newFakeClock()
	p := NewProbes(20*time.Second, clock.Now)
	clock.Advance(10 * time.Second)
	if p.Hit(ProbeStartup, true) {
		t.Error("startup à 10 s : attendu 503")
	}
	clock.Advance(10 * time.Second)
	if !p.Hit(ProbeStartup, true) {
		t.Error("startup à 20 s : attendu 200")
	}
}

func TestFailIsTemporary(t *testing.T) {
	for _, kind := range []ProbeKind{ProbeReady, ProbeLive} {
		t.Run(string(kind), func(t *testing.T) {
			clock := newFakeClock()
			p := NewProbes(0, clock.Now)
			if err := p.Fail(kind); err != nil {
				t.Fatal(err)
			}
			clock.Advance(59 * time.Second)
			if p.Hit(kind, true) {
				t.Error("à 59 s : attendu 503")
			}
			if s := p.Snapshot()[kind]; s.Healthy || s.FailingUntil.IsZero() {
				t.Errorf("état à 59 s : %+v", s)
			}
			clock.Advance(time.Second)
			if !p.Hit(kind, true) {
				t.Error("à 60 s : attendu 200")
			}
		})
	}
}

func TestFailStartup(t *testing.T) {
	p := NewProbes(0, time.Now)
	if err := p.Fail(ProbeStartup); !errors.Is(err, ErrNotFailable) {
		t.Errorf("Fail(startup) = %v, attendu ErrNotFailable", err)
	}
}

func TestOnlyKubeletCounts(t *testing.T) {
	p := NewProbes(0, time.Now)
	if !p.Hit(ProbeReady, false) {
		t.Error("un appel hors kubelet doit quand même répondre 200")
	}
	if n := p.Snapshot()[ProbeReady].Hits; n != 0 {
		t.Errorf("Hits = %d, attendu 0", n)
	}
}

func TestIntervalMeasured(t *testing.T) {
	clock := newFakeClock()
	p := NewProbes(0, clock.Now)
	for i := 0; i < 4; i++ {
		p.Hit(ProbeLive, true)
		clock.Advance(10 * time.Second)
	}
	s := p.Snapshot()[ProbeLive]
	if s.Hits != 4 || s.IntervalSeconds != 10 {
		t.Errorf("Hits = %d, IntervalSeconds = %v ; attendus 4 et 10", s.Hits, s.IntervalSeconds)
	}
}

func TestProbesConcurrent(t *testing.T) {
	p := NewProbes(0, time.Now)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p.Hit(ProbeReady, true)
				_ = p.Snapshot()
			}
		}()
	}
	wg.Wait()
	if n := p.Snapshot()[ProbeReady].Hits; n != 5000 {
		t.Errorf("Hits = %d, attendu 5000", n)
	}
}

func TestIsKubelet(t *testing.T) {
	if !IsKubelet("kube-probe/1.31") {
		t.Error("kube-probe/1.31 non reconnu")
	}
	if IsKubelet("curl/8.0") {
		t.Error("curl reconnu comme kubelet")
	}
}
