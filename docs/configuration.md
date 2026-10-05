# Configuration et routes

## Variables d'environnement

Toutes sont facultatives. Une valeur invalide arrête l'app au démarrage avec
un message qui nomme la variable fautive (code de sortie 2) ; le message ne
reprend jamais un mot de passe.

| Variable | Rôle | Défaut | Exemple |
|---|---|---|---|
| `PORT` | Port d'écoute. La NetworkPolicy de Cassiopée ne laisse passer l'ingress que vers **80, 8080, 443 et 8443** : sur un autre port, l'app démarre, journalise un avertissement et l'affiche en haut de la page, mais l'endpoint ne la joindra pas. | `8080` | `8080` |
| `CANARY_MODE` | `web` : serveur et page. `cron` : un battement envoyé à l'app web, puis sortie. Remplace une sous-commande, que le formulaire de l'intranet ne permet pas de passer. | `web` | `cron` |
| `DATABASE_URL` | Le `connection_string` rendu par Cassiopée à la création de la base, collé tel quel (les espaces autour sont ignorés). Le moteur se déduit du schéma : `postgresql://` ou `postgres://`, `mysql://` ou `mariadb://`, `mongodb://`. Vide : la carte Base indique « non configurée ». | vide | `postgresql://postgres:…@canary-db-postgresql:5432/app` |
| `STARTUP_DELAY` | Secondes pendant lesquelles `/startup` répond 503, pour voir la sonde startup retenir le pod. | `0` | `20` |
| `CANARY_REPORT_URL` | Mode cron : adresse de l'app web. Le battement part en `POST <adresse>/api/cron`. Vide : le cron journalise et sort. | vide | `http://canary:80` |
| `CRON_EXIT_CODE` | Mode cron : code de sortie (0 à 255), pour tester `restartPolicy`. | `0` | `1` |
| `CANARY_TOKEN` | Protège la page et l'API par une authentification Basic : le navigateur affiche sa fenêtre de connexion, l'identifiant est libre, le mot de passe est le jeton. Les sondes restent ouvertes, kubelet n'a pas le jeton. Le mode cron envoie le jeton s'il est défini. | vide (page ouverte) | `un-long-secret` |

Toute autre variable s'affiche sur la page : c'est le test des variables
d'env. Les variables injectées par Kubernetes (`KUBERNETES_*`, `*_SERVICE_HOST`,
`*_PORT…`) et par l'image (`PATH`, `HOME`, `HOSTNAME`, `SSL_CERT_FILE`) sont
rangées à part.

**Masquage.** Une variable dont le nom contient `PASSWORD`, `SECRET`, `TOKEN`
ou `KEY` s'affiche `••••`. Une valeur de forme `schéma://utilisateur:mot-de-passe@…`
garde tout sauf le mot de passe. Les en-têtes `Authorization` et `Cookie` sont
masqués de la même façon.

## Routes HTTP

Toutes les routes marchent **sous n'importe quel préfixe** : le serveur retire
ce qui précède la première route connue (`/canary/api/status` devient
`/api/status`). Un chemin qui ne correspond à aucune route sert la page.

| Méthode et chemin | Corps | Réponses |
|---|---|---|
| `GET /` | – | 200 : la page |
| `GET /startup` | – | 200, ou 503 pendant `STARTUP_DELAY` |
| `GET /ready` | – | 200, ou 503 pendant un échec forcé |
| `GET /healthz` | – | 200, ou 503 pendant un échec forcé (sonde liveness) |
| `GET /api/status` | – | 200 : l'instantané décrit plus bas |
| `POST /api/probes/ready` et `POST /api/probes/live` | – | 204 : la sonde échoue pendant **60 s**, puis se rétablit seule. 400 pour `startup` |
| `POST /api/upload` | octets quelconques | 200 `{"bytes": n}` ; 413 au-delà de 50 Mo |
| `POST /api/db/write` | – | 204 : une ligne écrite dans `canary_hits` ; 409 sans base ; 502 et le message de la base en cas d'échec |
| `POST /api/db/credentials` | `{"user": "…", "password": "…"}` (1 Ko max) | 200 : rapport ci-dessous ; 400 si un champ est vide ou le JSON invalide ; 413 au-delà de 1 Ko ; 409 sans base |
| `POST /api/cron` | `{"pod": "…", "exitCode": 0, "sentAt": "…"}` (1 Ko max) | 204 ; 400 ou 413 comme ci-dessus |
| `GET /ws` | – | WebSocket : renvoie chaque message reçu. Le serveur n'envoie jamais de ping de lui-même. |

Avec `CANARY_TOKEN`, toutes les routes sauf les trois sondes répondent 401 sans
le jeton.

Les sondes ne comptent que les appels de kubelet, reconnus à leur User-Agent
`kube-probe/…`. Un `curl` reçoit la même réponse, mais n'apparaît pas dans les
compteurs.

### Rapport d'identifiants

`POST /api/db/credentials` se connecte avec ces identifiants **à l'hôte, au
port et à la base de `DATABASE_URL`**, jamais ailleurs, puis tente quatre
opérations de droits croissants, chacune limitée à 5 s :

```json
{
  "connect": {"ok": true},
  "read":    {"ok": true},
  "write":   {"ok": false, "error": "ERROR: permission denied for table canary_hits (SQLSTATE 42501)"},
  "ddl":     {"ok": false, "error": "ERROR: permission denied for schema public (SQLSTATE 42501)"}
}
```

| Étape | SQL (PostgreSQL, MariaDB) | MongoDB |
|---|---|---|
| `connect` | connexion | ping |
| `read` | `SELECT COUNT(*) FROM canary_hits` | `countDocuments` sur `canary_hits` |
| `write` | insertion d'une ligne `credentials-test` | insertion d'un document |
| `ddl` | `CREATE TABLE canary_ddl_probe_<n>` puis `DROP TABLE` | création puis suppression d'une collection |

Si la connexion échoue, les trois autres étapes valent
`{"ok": false, "error": "non tenté"}`.

## Contenu de `GET /api/status`

Un seul instantané, servi par **un seul pod** : avec plusieurs répliques, des
appels séparés tomberaient sur des pods différents et mélangeraient leurs
données.

| Champ | Contenu |
|---|---|
| `pod`, `version`, `startedAt`, `uptimeSeconds` | identité du pod et de l'image |
| `port`, `portAllowed` | port d'écoute, et s'il passe la NetworkPolicy |
| `requestPath` | chemin reçu par le pod, préfixe compris : montre ce que l'ingress transmet |
| `env` | variables d'environnement, masquées |
| `headers` | en-têtes de cette requête, masqués, `Host` compris |
| `runtime` | `uid`, `gid`, `namespace`, `deployment` (deviné du nom du pod), `cpuMillicores`, `cpuLimited`, `memoryMax`, `memoryCurrent`, `memoryLimited`, `rootWritable`, `tmpWritable` |
| `probes` | par sonde (`startup`, `ready`, `live`) : `hits` (appels kubelet), `lastHit`, `intervalSeconds` (écart moyen des 10 derniers appels), `failingUntil`, `healthy` |
| `db` | `null` sans base, sinon `engine`, `version`, `user`, `database`, `reachable`, `since` (début de l'état courant), `lastError`, `authFailed`, `writeOk`, `rows`, `lastWrite` |
| `egress` | `null` avant la première mesure, sinon `internalHost`, `internalDns`, `externalDns`, `https`, `checkedAt`. Mesuré toutes les 30 s en tâche de fond. |
| `cron` | les 20 derniers battements reçus, le plus récent d'abord (`[]` si aucun) |
| `checks` | les vérifications 2 à 8, 10 et 11 : `id`, `name`, `star`, `level` (`ok`, `warn`, `ko`, `na`), `detail` |

## Les vérifications

La page ajoute la 1 et la 9, qui ne se constatent que depuis le navigateur. Le
compteur de l'en-tête vaut « vertes / configurées » : une vérification grise
n'y entre pas. Une étoile prend le pire niveau de ses vérifications
configurées.

| # | Vérification | Vert | Orange | Rouge | Gris | Étoile |
|---|---|---|---|---|---|---|
| 1 | Serveur joignable | `/api/status` répond | – | pas de réponse | – | Application |
| 2 | Non-root | uid ≠ 0 | – | uid 0 | – | Application |
| 3 | Limites appliquées | `cpu.max` et `memory.max` bornés | l'une des deux illimitée | – | – | Application |
| 4 | Sonde startup | appelée par kubelet | – | – | jamais appelée | Sondes |
| 5 | Sonde readiness | dernier appel il y a moins de 3 intervalles | plus appelée, ou échec forcé en cours | – | jamais appelée | Sondes |
| 6 | Sonde liveness | idem | idem | – | jamais appelée | Sondes |
| 7 | Passage par l'ingress | `X-Forwarded-For` présent | – | – | accès direct | Endpoint |
| 8 | HTTPS | `X-Forwarded-Proto: https` | `http` | – | accès direct | Endpoint |
| 9 | WebSocket | écho reçu | – | connexion refusée ou coupée | connexion en cours | Endpoint |
| 10 | Base de données | joignable, écriture et relecture réussies | écriture en échec, ou injoignable depuis moins de 60 s | injoignable depuis 60 s ou plus, ou authentification refusée | pas de `DATABASE_URL` | Base |
| 11 | DNS | DNS interne et externe résolus | – | l'un des deux en échec | pas encore mesuré | Réseau |

Le HTTPS sortant (vers `https://example.com`) s'affiche sans voyant : la
sortie vers Internet est un réglage de SiOps, la trouver fermée n'est pas une
panne.
