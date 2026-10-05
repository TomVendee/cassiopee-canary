package main

import (
	"net/http"
	"strings"
)

// Masked remplace toute valeur secrète affichée sur la page.
const Masked = "••••"

// secretNameParts : une variable dont le nom contient l'un de ces mots est
// masquée entièrement. La page est publique : on préfère masquer trop.
var secretNameParts = []string{"PASS", "PWD", "SECRET", "TOKEN", "KEY", "CREDENTIAL"}

// secretHeaders sont masqués dans l'affichage des en-têtes de requête.
var secretHeaders = map[string]bool{"Authorization": true, "Cookie": true}

// MaskEnv transforme la liste « NOM=valeur » d'os.Environ en table affichable :
// valeurs secrètes masquées, mot de passe retiré des URL.
func MaskEnv(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isSecretName(name) {
			out[name] = Masked
		} else {
			out[name] = MaskURL(value)
		}
	}
	return out
}

func isSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, part := range secretNameParts {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return false
}

// MaskURL remplace le mot de passe d'une URL « schéma://user:pass@hôte… » par
// Masked. Toute autre valeur revient inchangée.
//
// Volontairement sans url.Parse : un copier-coller avec un espace ou un retour
// à la ligne, ou un mot de passe mal échappé, fait échouer l'analyse, et la
// valeur fuirait telle quelle. Ici on coupe au dernier « @ » : dans le doute,
// on masque trop plutôt que pas assez.
func MaskURL(raw string) string {
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return raw
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return raw
	}
	user, _, hasPassword := strings.Cut(rest[:at], ":")
	if !hasPassword {
		return raw
	}
	return scheme + "://" + user + ":" + Masked + rest[at:]
}

// MaskHeaders aplatit les en-têtes d'une requête pour l'affichage, en masquant
// Authorization et Cookie.
func MaskHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for name, values := range h {
		if secretHeaders[name] {
			out[name] = Masked
			continue
		}
		out[name] = strings.Join(values, ", ")
	}
	return out
}
