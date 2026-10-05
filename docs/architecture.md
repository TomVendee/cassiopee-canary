# Architecture

Un seul binaire Go, un seul paquet (`main`), aucun état hors de la base. La
page est embarquée dans le binaire ; l'image finale ne contient que lui.

## Un fichier, une responsabilité

| Fichier | Rôle |
|---|---|
| `main.go` | Point d'entrée : lit la config, choisit le mode (web ou cron), démarre les tâches de fond, arrête proprement sur SIGTERM |
| `config.go` | Variables d'env → `Config` ; lecture du `connection_string` en `DBTarget` |
| `mask.go` | Masquage des secrets : variables, URL, en-têtes |
| `runtime.go` | Ce que le conteneur voit de lui-même : uid, cgroup (CPU, mémoire), namespace, droits d'écriture |
| `probes.go` | Les trois sondes : compteurs d'appels kubelet, intervalle mesuré, échecs forcés temporisés |
| `server.go` | Routes, retrait du préfixe, authentification, page embarquée |
| `status.go` | `GET /api/status` : assemble l'instantané |
| `checks.go` | Niveau de chaque vérification calculable côté serveur |
| `db.go` | Interface `Store`, choix du moteur, surveillance de la base (`Monitor`) |
| `db_sql.go` | Ce que PostgreSQL et MariaDB ont en commun, par `database/sql` |
| `db_postgres.go`, `db_mysql.go` | Ce qui les distingue : pilote, SQL, reconnaissance d'un refus d'authentification |
| `db_mongo.go` | MongoDB |
| `egress.go` | Réseau sortant : DNS interne, DNS externe, HTTPS |
| `cron.go` | Mode cron (envoi du battement) et réception côté web |
| `ws.go` | Écho WebSocket |
| `web/index.html` | La page : HTML, CSS et JavaScript sans framework ni ressource externe |

## Parcours d'une requête

```
navigateur ──https──▶ ingress ──http──▶ pod :8080
                                          │
                     stripPrefix          │  /canary/api/status → /api/status
                                          │  (le chemin d'origine reste dans le contexte)
                     requireToken         │  401 sans le jeton, sauf les sondes
                                          │  (seulement si CANARY_TOKEN est défini)
                     ServeMux             │  routage par méthode et chemin
                                          ▼
                     buildStatus ── Probes.Snapshot ─┐
                                 ── Monitor.Status ──┤  lectures en mémoire,
                                 ── EgressWatcher ───┤  jamais d'appel réseau
                                 ── CronLog.List ────┤  pendant la requête
                                 ── ReadRuntime ─────┘
                                 ── Evaluate → vérifications
```

`GET /api/status` ne fait aucun appel réseau : la base et le réseau sortant
sont mesurés en tâche de fond. Une base suspendue ou une sortie fermée ne
ralentit donc jamais la page.

## Les tâches de fond

```
main ─┬─ go Monitor.Run(ctx, 5 s)        ping de la base ; à chaque (re)connexion,
      │                                  création de la table et écriture d'une ligne
      ├─ go EgressWatcher.Run(ctx, 30 s) DNS interne, DNS externe, HTTPS sortant
      └─ http.Server                     une goroutine par requête (fournie par net/http)

SIGTERM ─▶ ctx annulé ─▶ les boucles s'arrêtent, Shutdown laisse 10 s aux requêtes
```

À chaque tour, `Monitor` met à jour un `DBStatus`. `Since` marque le début de
l'état courant : c'est ce qui permet de dire « injoignable depuis 32 s » et de
passer de l'orange au rouge à 60 s. L'écriture n'a lieu qu'à la reconnexion,
pour ne pas remplir la table toutes les 5 s, mais elle prouve à chaque retour
de la base que l'écriture marche encore.

## Pourquoi ces choix

**Port 8080.** La NetworkPolicy que SiOps pose dans chaque namespace
(`network_policy_service.py`) n'accepte le trafic de l'ingress que vers 80,
8080, 443 et 8443. Sur un autre port, tout semble déployé, mais l'endpoint
répond 502 ou 504.

**`EXPOSE 8080` dans le Dockerfile.** Si le port cible du service est absent,
SiOps lit le port déclaré par l'image, en anonyme, sur le registre
(`registry_service.py`). L'intranet, lui, envoie toujours un port (80 par
défaut, voir la recette) : l'`EXPOSE` ne sert donc qu'aux appels directs à
l'API de Cassiopée, et la détection ne se teste pas depuis l'intranet.

**Image distroless, `USER 1000:1000`.** Le formulaire de l'intranet impose
`runAsNonRoot` et `runAsUser: 1000`. L'image ne contient ni shell ni
gestionnaire de paquets (environ 17 Mo), et fonctionne aussi avec
`readOnlyRootFilesystem` : elle n'écrit rien sur le disque.

**Mode cron par variable d'env.** Le modèle d'application de Cassiopée n'a
pas de champ `command` ni `args` : impossible de lancer `canary cron`. Le
battement passe en HTTP par le service interne plutôt que par la base, pour
que le passage CronJob tienne dans le quota de trois ressources (app,
endpoint, CronJob).

**Une seule route d'état.** Avec plusieurs répliques, quatre appels séparés
toutes les 2 s tomberaient sur quatre pods différents, et la page mélangerait
leurs données. Un seul appel, c'est une seule photographie d'un seul pod.

**Échec de sonde temporisé (60 s).** Avec une seule réplique, une readiness en
échec retire le pod du service : la page devient injoignable, et personne ne
pourrait plus cliquer pour rétablir. L'échec s'annule donc seul.

**Pas de ping WebSocket côté serveur.** Le test de silence mesure le
`proxyReadTimeout` de l'ingress. Si le serveur envoyait des pings, la
connexion ne serait jamais inactive et le test ne mesurerait rien.

**Formulaire d'identifiants limité à l'hôte de `DATABASE_URL`.** Le formulaire
ne prend qu'un utilisateur et un mot de passe : on ne peut pas s'en servir pour
sonder d'autres machines du cluster depuis Internet.

**Préfixe retiré côté serveur, URL relatives côté page.** L'endpoint peut
publier l'app sous `/` comme sous `/canary`, avec ou sans `rewrite-target`.
La page calcule sa base à partir de `location.pathname` en ajoutant toujours
une barre finale : sans elle, `/canary` + `api/status` partirait vers
`/api/status`, hors du préfixe.

**Horloge injectée.** Toute logique qui dépend de l'heure (sondes, base,
vérifications) reçoit une fonction `now`. Les tests avancent une horloge
factice au lieu d'attendre 60 vraies secondes.

**`CGO_ENABLED=0`.** Le binaire est statique : il tourne dans une image sans
bibliothèque C. Les tests, eux, utilisent `-race`, qui a besoin de cgo : c'est
pourquoi ils tournent avec la chaîne Go complète, pas dans l'image.
