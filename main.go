// Commande canary : application témoin à déployer avec Cassiopée. Voir
// README.md pour l'usage et docs/ pour le détail.
package main

import (
	"fmt"
	"os"
)

// version est remplacée à la compilation par -ldflags "-X main.version=…"
// (voir Dockerfile). Elle s'affiche sur la page : on voit ainsi quelle image
// tourne réellement après un changement de tag dans l'intranet.
var version = "dev"

func main() {
	cfg, err := Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration invalide :", err)
		os.Exit(2)
	}
	_ = cfg
}
