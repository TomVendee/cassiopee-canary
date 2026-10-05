package main

import (
	"net/http"
	"net/url"
	"strings"
)

// Masked remplace toute valeur secrète affichée sur la page.
const Masked = "••••"

// secretNameParts : une variable dont le nom contient l'un de ces mots est
// masquée entièrement.
var secretNameParts = []string{"PASSWORD", "SECRET", "TOKEN", "KEY"}

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
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	// On remplace dans la chaîne d'origine plutôt que de passer par
	// u.String(), qui encoderait les puces en %E2%80%A2.
	scheme, rest, _ := strings.Cut(raw, "://")
	userinfo, after, found := strings.Cut(rest, "@")
	if !found {
		return raw
	}
	user, _, _ := strings.Cut(userinfo, ":")
	return scheme + "://" + user + ":" + Masked + "@" + after
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
