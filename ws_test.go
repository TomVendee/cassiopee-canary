package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dialWS(t *testing.T, path string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(NewServer(testDeps(Config{})))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, nil)
	if err != nil {
		t.Fatalf("Dial %s : %v", path, err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func TestWSEcho(t *testing.T) {
	c := dialWS(t, "/canary/ws")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, msg := range []struct {
		typ  websocket.MessageType
		data string
	}{{websocket.MessageText, "hello"}, {websocket.MessageBinary, "\x00\x01"}} {
		if err := c.Write(ctx, msg.typ, []byte(msg.data)); err != nil {
			t.Fatal(err)
		}
		typ, got, err := c.Read(ctx)
		if err != nil || typ != msg.typ || string(got) != msg.data {
			t.Errorf("écho = %v %q %v ; attendu %v %q", typ, got, err, msg.typ, msg.data)
		}
	}
}

func TestWSNoServerPing(t *testing.T) {
	c := dialWS(t, "/ws")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Read traite les pings de contrôle sans les rendre : seul un message de
	// données ou l'expiration du délai le débloque.
	_, data, err := c.Read(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("reçu %q, %v ; attendu le silence jusqu'au délai", data, err)
	}
}
