package main

import (
	"net/http"

	"github.com/coder/websocket"
)

// handleWS renvoie chaque message reçu, tel quel. Le serveur n'envoie jamais
// de ping de lui-même : pendant le test de silence, rien ne circule, et la
// coupure observée par la page est donc bien celle du proxyReadTimeout de
// l'ingress.
//
// Accept refuse par défaut les origines étrangères ; c'est ce qu'on veut, la
// page et le WebSocket étant servis par le même hôte.
func handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept a déjà répondu au client
	}
	defer c.CloseNow()
	ctx := r.Context()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return // fermeture par le client, par l'ingress, ou arrêt du serveur
		}
		if err := c.Write(ctx, typ, data); err != nil {
			return
		}
	}
}
