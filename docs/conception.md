> Copie de la conception validée le 5 octobre 2026 avant d'écrire le code
> (source : `docs/superpowers/specs/2026-10-05-cassiopee-canary-design.md` du
> dépôt `local-environment` de SIWeb). Le texte d'origine suit, inchangé ; les
> écarts décidés pendant l'implémentation sont listés juste en dessous.

## Écarts constatés pendant l'implémentation

- **Sondes en échec forcé** : readiness et liveness passent à l'orange pendant
  un échec forcé. La règle d'origine (§7) ne regardait que la régularité des
  appels de kubelet, et une sonde qui répondait 503 restait verte.
- **`/api/status`** porte aussi `requestPath` (chemin reçu, préfixe compris) et
  l'en-tête `Host`, nécessaires à la carte Endpoint (§8).
- **Test DDL** : la table créée puis supprimée s'appelle
  `canary_ddl_probe_<n>`, avec un suffixe unique, pour qu'un reste d'essai
  interrompu ne fausse pas le résultat.
- **Port et détection par `EXPOSE`** : le formulaire de l'intranet envoie
  toujours un port de conteneur (80 si la section Ports est vide) et un port
  cible (80 par défaut, alors que le champ affiche 8080 en grisé). Le plan de
  recette (§12) fait donc saisir 8080 dans les deux champs, et la question
  n° 8 du §14 n'est pas testable depuis l'intranet. Pour la même raison, la
  méthode Standard ne peut servir que des images qui écoutent sur 80.
- **Ordre du passage 1** : application, puis endpoint, puis base, et enfin
  `DATABASE_URL` ajoutée à l'application. Sur staging, un compte sans
  ressource ne pouvait pas créer de base avant qu'une application ait fait
  créer son namespace.
- **Annotation testée** : `proxy-body-size` relevée à `20m` (et non abaissée à
  `2m`), parce que la limite par défaut d'ingress-nginx est déjà de 1 Mo.
- **Page sur mobile** : sous 700 px, la constellation masque ses étiquettes et
  grossit ses étoiles ; la légende juste dessous reprend les noms.
- **`compose.yaml`** applique au conteneur les limites de l'intranet (200m de
  CPU, 256 Mio), pour que la démo locale ressemble à la prod.

---

# Cassiopée Canary — conception

Date : 5 octobre 2026
Statut : validé en séance, section par section ; en attente de relecture de la spec.

## 1. Objet

Une **application témoin** que l'on déploie **avec Cassiopée, depuis l'intranet
prod** (`intranet.sigl.epita.fr`), exactement comme un étudiant le ferait. Une
fois en ligne, sa page web montre, voyant par voyant, que chaque fonctionnalité
de Cassiopée a réellement pris effet dans le cluster : variables d'env, ports,
répliques, sondes, limites, securityContext, endpoint (TLS, chemin, WebSocket,
annotations), bases de données (trois moteurs, utilisateurs, rotation,
suspension) et CronJob.

Ce n'est **pas** un client de l'API Cassiopée : l'app ne parle jamais au relais
ni à SiOps. On la pilote par les formulaires de l'intranet ; elle observe le
résultat de l'intérieur.

Périmètre : **applications, endpoints, bases de données**. Hors périmètre : S3
(SiOps répond 503 en staging comme en prod, `OPENSTACK_PASSWORD` absent),
backups (non relayés par `cassiopee_back`), volumes persistants (aucun champ).

## 2. Contexte et contraintes

Légende : **[mesuré]** = constaté par une requête ; **[code]** = lu dans le code
SiOps (`ing/majeures/sigl/siops/cassiopee`, `main`, commit `99578cc`), non mesuré.

- Chaîne prod : front `intranet.sigl.epita.fr` → relais
  `intranet.sigl.epita.fr/api/cassiopee-back` (branche `main` de
  `cassiopee_back`) → SiOps `cassiopee.siops.sigl.epita.fr`, realm `SIWEB-Prod`
  **[mesuré, bundle du front et health le 05/10]**.
- **Quota de 3 ressources** par utilisateur, applications + bases + endpoints
  confondus ; dépassement en 429 **[mesuré le 15/09 sur staging]**.
- **L'image doit être publique** : le modèle d'application n'a pas
  d'`imagePullSecrets` ; les images GHCR de l'org `SIGL-SIWEB` sont privées (403
  anonyme **[mesuré]**). Image retenue : `ghcr.io/tomvendee/cassiopee-canary`.
- **NetworkPolicy** : entrée autorisée depuis l'ingress controller sur les ports
  **80, 8080, 443, 8443** seulement ; trafic libre dans le même namespace ; DNS
  autorisé ; sortie externe optionnelle (`network_policy_allow_external_egress`)
  **[code, `network_policy_service.py`]**. L'app écoute donc sur **8080**.
- **Port détecté** : si `target_port` est omis, SiOps lit l'`EXPOSE` de l'image
  en anonyme sur le registre **[code, `registry_service.py`]**. Le Dockerfile
  déclare `EXPOSE 8080`.
- **Pas de champ `command`/`args`** dans le modèle d'application
  **[Swagger]** : le mode (web ou cron) passe par une variable d'env.
- Formulaire de création de l'intranet **[code `intra` `main`]** : limites
  fixées en dur à `200m` CPU / `256Mi` ; `securityContext` fixé à
  `runAsNonRoot: true`, `runAsUser: 1000`, `readOnlyRootFilesystem: false` ;
  placeholders des sondes `/healthz`, `/ready`, `/startup` ; moteurs
  PostgreSQL, MariaDB, MongoDB.
- Bases **[code, `database_service.py`]** :
  - `connection_string` : `postgresql://u:p@h:5432/db`,
    `mysql://u:p@h:3306/db` (MariaDB), `mongodb://u:p@h:27017/db?authSource=admin` ;
  - hôte = `<nom>-<moteur>`, nom court de service, même namespace que l'app ;
  - mots de passe `secrets.token_urlsafe(32)` (43 caractères URL-safe) ;
  - la **rotation** ne réécrit que le secret Vault ; le message dit « You may
    need to restart the database pod » — l'effet réel sur la base est inconnu ;
  - les permissions `readOnly | readWrite | admin` sont appliquées par le chart
    Helm (non lu) — effet réel inconnu ;
  - suppression : « 7-day grace period ».
- Statuts asynchrones (Git puis ArgoCD) : `deployed` en ~40 s à 1 min 15
  **[mesuré sur staging]**.

## 3. Choix structurants

| Choix | Retenu | Écarté, et pourquoi |
|---|---|---|
| Langage | **Go 1.27** | Node : plus familier à l'équipe, mais le but est aussi de découvrir Go ; image et mémoire bien plus légères en Go |
| Livraison | Un binaire, page embarquée (`//go:embed`) | Front séparé : deux artefacts pour une page |
| Image | `gcr.io/distroless/static`, `USER 1000:1000`, ~15 Mo, amd64 seul | Alpine : shell inutile ; multi-arch : le cluster est amd64 |
| Dépôt | `TomVendee/cassiopee-canary`, public | Org `SIGL-SIWEB` : paquets privés |
| Build | GitHub Actions → GHCR avec `GITHUB_TOKEN` | Push local : scope `write:packages` à ajouter, manuel |
| État | Aucun hors base ; compteurs en mémoire | Persistance : la remise à zéro au redémarrage *est* la preuve de la liveness |
| Pilotes | `pgx` v5, `go-sql-driver/mysql`, `mongo-driver` v2, `coder/websocket` | WebSocket fait main : fragile. Versions et API vérifiées dans leur doc au moment de coder |

## 4. Architecture

Un binaire `canary`, deux modes choisis par `CANARY_MODE`.

- **`web`** (défaut) : serveur HTTP sur `PORT`. Sert la page, une API JSON, les
  sondes, le WebSocket. Une goroutine teste la base toutes les 5 s.
- **`cron`** : envoie un battement en HTTP à l'app web (`CANARY_REPORT_URL`),
  puis se termine avec `CRON_EXIT_CODE`. Aucune base requise : le passage
  CronJob tient ainsi dans le quota (app + endpoint + CronJob).

Organisation du dépôt :

```
main.go            point d'entrée : mode, config, démarrage du serveur ou du cron
config.go          lecture et validation des variables d'env
mask.go            masquage des secrets (env, URL, en-têtes)
server.go          routes, retrait du préfixe, authentification optionnelle
probes.go          sondes : compteurs, intervalle mesuré, échecs temporisés
runtime.go         pod, uid/gid, cgroup (CPU/mémoire), écriture sur / et /tmp
checks.go          niveau de chaque vérification calculable côté serveur
status.go          GET /api/status : un instantané complet, servi par un seul pod
db.go              interface Store + détection du moteur + boucle de santé
db_postgres.go     implémentation PostgreSQL (pgx)
db_mysql.go        implémentation MariaDB (go-sql-driver/mysql)
db_mongo.go        implémentation MongoDB (mongo-driver v2)
ws.go              écho WebSocket
egress.go          DNS interne/externe, HTTPS sortant
cron.go            mode cron et réception des battements
web/index.html     la page (HTML/CSS/JS sans framework, SVG inline)
*_test.go          tests unitaires ; *_integration_test.go (build tag integration)
compose.yaml       PostgreSQL + MariaDB + MongoDB + canary, pour le local
Dockerfile         compilation dans golang, image finale distroless
.github/workflows/build.yml
README.md, docs/
```

`Store` est l'interface commune aux trois moteurs :

```go
type Store interface {
    Engine() string                       // "postgresql" | "mariadb" | "mongodb"
    Ping(ctx context.Context) (version string, err error)
    EnsureSchema(ctx context.Context) error        // crée canary_hits si absente
    Write(ctx context.Context, pod string) error   // une ligne dans canary_hits
    Count(ctx context.Context) (int64, time.Time, error)
    TryCredentials(ctx context.Context, user, password string) CredentialReport
    IsAuthError(err error) bool                    // mot de passe refusé ?
    Close() error
}
```

## 5. Contrat de configuration

| Variable | Rôle | Défaut |
|---|---|---|
| `PORT` | Port d'écoute. Doit valoir 80, 8080, 443 ou 8443 (NetworkPolicy) ; autre valeur : l'app démarre mais la page affiche un avertissement | `8080` |
| `CANARY_MODE` | `web` ou `cron` ; autre valeur : arrêt avec erreur explicite | `web` |
| `DATABASE_URL` | Le `connection_string` rendu à la création de la base, collé tel quel. Moteur déduit du schéma : `postgresql://`/`postgres://`, `mysql://`/`mariadb://`, `mongodb://` | vide → carte Base « non configurée » |
| `STARTUP_DELAY` | Secondes pendant lesquelles `/startup` répond 503 | `0` |
| `CANARY_REPORT_URL` | Mode cron : URL de l'app web, ex. `http://canary:80` ; le battement part en `POST <url>/api/cron` | vide → le cron journalise et sort |
| `CRON_EXIT_CODE` | Mode cron : code de sortie, pour tester `restartPolicy` | `0` |
| `CANARY_TOKEN` | Si défini : authentification Basic native du navigateur (mot de passe = jeton, identifiant libre) sur la page et l'API. Exemptés : `/startup`, `/ready`, `/healthz`. Le mode cron l'envoie s'il est défini | vide → page ouverte |

Toute autre variable est affichée (test des variables d'env). **Masquage** :
valeur remplacée par `••••` si le nom contient `PASSWORD`, `SECRET`, `TOKEN` ou
`KEY` ; mot de passe masqué dans toute valeur de forme `scheme://user:pass@…`
(donc `DATABASE_URL`). Les en-têtes `Authorization` et `Cookie` sont masqués de
même dans l'affichage des requêtes.

## 6. Routes de l'app

Toutes fonctionnent **sous n'importe quel préfixe** : le serveur cherche la
première route connue dans le chemin et ignore ce qui précède (`/canary/api/status`
→ `/api/status`). La page n'utilise que des URL relatives. L'endpoint marche donc
avec `path: /` comme avec `path: /canary`, avec ou sans `rewrite-target`.

| Route | Effet |
|---|---|
| `GET /` | La page |
| `GET /startup`, `/ready`, `/healthz` | Sondes. Comptent les appels de kubelet (User-Agent `kube-probe/…`) ; 200 ou 503 selon l'état |
| `GET /api/status` | **Un seul instantané, servi par un seul pod** : pod, version, uptime, port d'écoute ; env masqué ; runtime (uid, gid, cgroup, écriture disque) ; en-têtes de *cette* requête ; sondes (appels kubelet, dernier appel, intervalle moyen, échec forcé) ; base (moteur, version, utilisateur, joignable ou injoignable depuis, dernière erreur, lignes, dernière écriture) ; réseau sortant ; battements du cron ; niveau de chaque vérification serveur (section 7). Une route unique plutôt que quatre : avec plusieurs répliques, des appels séparés tomberaient sur des pods différents et mélangeraient leurs données |
| `POST /api/probes/{ready\|live}` | Fait échouer la sonde **60 s**, puis rétablit seule. Indispensable : avec une réplique, une readiness en échec retire le pod du service et rend la page injoignable — l'utilisateur ne pourrait plus rien rétablir |
| `POST /api/db/write` | Insère une ligne `(pod, horodatage)` dans `canary_hits` |
| `POST /api/db/credentials` | Corps `{user, password}`. Tente connexion, lecture, écriture, DDL avec ces identifiants, **uniquement sur l'hôte, le port et la base de `DATABASE_URL`** (pas d'hôte libre : pas de SSRF). Délai 5 s par étape |
| `POST /api/upload` | Lit et jette le corps, rend la taille reçue. Plafond serveur 50 Mo |
| `POST /api/cron` | Reçoit un battement `{pod, exitCode, sentAt}` ; garde les 20 derniers en mémoire |
| `GET /ws` | WebSocket : renvoie chaque message reçu. Le serveur n'envoie **aucun** ping, pour que le test de silence mesure bien le `proxyReadTimeout` de l'ingress |

Tables créées au démarrage si absentes, avec l'utilisateur principal :
`canary_hits(id, pod, created_at)` (PostgreSQL, MariaDB), collection
`canary_hits` (MongoDB). Le test DDL du formulaire crée puis supprime
`canary_ddl_probe`.

## 7. Vérifications et voyants

Voyants : 🟢 OK · 🟠 dégradé · 🔴 KO · ⚪ non configuré (exclu du compteur).

| # | Vérification | 🟢 si | 🟠 / 🔴 | Étoile |
|---|---|---|---|---|
| 1 | Serveur joignable | `/api/status` répond | 🔴 sinon (la page l'affiche côté client) | Application |
| 2 | Non-root | uid ≠ 0 | 🔴 uid 0 | Application |
| 3 | Limites appliquées | `memory.max` et `cpu.max` lus et bornés | 🟠 « max » (aucune limite) | Application |
| 4 | Sonde startup | appelée par kubelet | ⚪ jamais appelée | Sondes |
| 5 | Sonde readiness | dernier appel < 3 × intervalle mesuré | 🟠 plus appelée ; ⚪ jamais | Sondes |
| 6 | Sonde liveness | idem | idem | Sondes |
| 7 | Passage par l'ingress | `X-Forwarded-For` présent | ⚪ accès direct (local, port-forward) | Endpoint |
| 8 | HTTPS | `X-Forwarded-Proto: https` | 🟠 `http` | Endpoint |
| 9 | WebSocket | écho reçu | 🔴 échec de connexion | Endpoint |
| 10 | Base | ping OK et écriture + relecture OK | 🟠 injoignable < 60 s (suspension, redémarrage) ; 🔴 ≥ 60 s ou authentification refusée ; ⚪ pas de `DATABASE_URL` | Base |
| 11 | DNS | interne et externe résolus | 🔴 sinon | Réseau |

Une **étoile** est verte si toutes ses vérifications configurées sont vertes,
orange si l'une est orange, rouge si l'une est rouge, grise si aucune n'est
configurée. Le compteur d'en-tête vaut « vertes / configurées ».

Réseau sortant, rafraîchi en tâche de fond toutes les 30 s : DNS interne (hôte
de la base, sinon `kubernetes.default.svc`), DNS externe (`example.com`),
`GET https://example.com` (délai 3 s).

Les vérifications 1 (serveur joignable) et 9 (WebSocket) ne se constatent que
dans le navigateur : le serveur rend le niveau des neuf autres, la page ajoute
ces deux-là et calcule étoiles et compteur (le pire niveau l'emporte).

Informations affichées sans voyant : variables d'env, en-têtes, uid/gid,
écriture sur `/` et `/tmp`, RAM utilisée, HTTPS sortant (ouvert ou fermé : c'est
une politique SiOps, pas une panne), battements du CronJob.

Tests déclenchés à la main, résultat affiché dans la carte : rafale ×20 (pods
distincts et répartition), envoi 1 / 5 / 10 Mo (révèle `proxy-body-size`),
silence WebSocket de 90 s (instant de coupure), écriture en base, formulaire
d'identifiants (connexion / lecture / écriture / DDL), échec temporisé des
sondes.

**Plusieurs répliques** : chaque réponse porte le nom du pod. Les compteurs
sont propres au pod qui répond, et la carte l'indique. C'est voulu.

## 8. La page

Une page, thème **sombre uniquement** (« ciel de nuit »), sans framework ni
dépendance externe (pas de CDN, pas de police distante : le binaire suffit).

- **En-tête** : nom, pod, version, uptime, URL ; compteur « 9 / 11 » ; la
  **constellation de Cassiopée** en W, cinq étoiles = Application, Sondes,
  Endpoint, Base, Réseau, colorées selon la section 7.
- **Grille de cartes** : Application, Sondes, Endpoint, Variables d'env,
  Ressources, WebSocket, Sécurité, Base de données, Identifiants, Réseau
  sortant, CronJob.
- **Mouvement sobre** : fondu à l'allumage d'une étoile ; tracé
  « électrocardiogramme » des sondes avançant en temps réel avec un pic par
  appel kubelet ; anneau WebSocket rempli seconde par seconde pendant le silence ;
  puces de pods apparaissant pendant la rafale. Tout est désactivé sous
  `prefers-reduced-motion`.
- Rafraîchissement toutes les 2 s par `GET /api/status`. Une erreur réseau passe la page en « injoignable depuis N s »
  sans la vider.
- Lisible sur mobile (une colonne sous 700 px).

Maquette validée en séance (constellation + grille de cartes).

## 9. Build et publication

- `Dockerfile` multi-étapes : `golang:1.27` compile avec `CGO_ENABLED=0`,
  `-trimpath`, `-ldflags "-s -w -X main.version=$VERSION"` ; image finale
  `gcr.io/distroless/static`, `USER 1000:1000`, `EXPOSE 8080`.
- `.github/workflows/build.yml` :
  - PR : `gofmt -l` (doit être vide), `go vet`, `go test`, tests d'intégration
    avec conteneurs de service PostgreSQL, MariaDB, MongoDB ;
  - push sur `main` : idem, puis image poussée en `latest` et `sha-<court>` ;
  - tag `vX.Y.Z` : image `X.Y.Z`.
  - Permissions `contents: read`, `packages: write` ; `GITHUB_TOKEN` seul.
- **Étape manuelle, une fois** : passer le paquet GHCR en public (réglages du
  paquet sur GitHub ; l'API REST ne le permet pas). Documentée dans le README.

## 10. Tests

- **Unitaires** (`testing`, tests en tableaux de cas) : détection du moteur ;
  conversion `mysql://u:p@h:3306/db` → DSN `u:p@tcp(h:3306)/db?parseTime=true` ;
  masquage ; retrait du préfixe ; sondes (délai de démarrage, échec temporisé de
  60 s puis rétablissement, intervalle mesuré) ; lecture du cgroup
  (`cpu.max` « 20000 100000 » → 200m, « max 100000 » → illimité) ; refus d'un
  hôte différent dans le formulaire ; calcul des voyants et des étoiles.
- **Intégration** (`//go:build integration`) : pour chaque moteur, connexion,
  écriture, relecture, et un utilisateur en lecture seule créé par le test, à
  qui l'écriture est refusée. En CI par conteneurs de service ; en local par
  `docker compose up`.
- **Page** : vérifiée à la main dans un navigateur contre `compose.yaml`.

## 11. Documentation

En français. Identifiants en anglais.

- `README.md` : ce que c'est, démarrage local en 5 minutes, publication, liens.
- `docs/recette.md` : le guide pas à pas depuis l'intranet (section 12),
  champ par champ, résultat attendu sur la page, et ce qu'un échec veut dire
  (relais, SiOps ou cluster).
- `docs/configuration.md` : variables d'env et routes.
- `docs/architecture.md` : organisation, parcours d'une requête, raison de
  chaque choix.
- `docs/decouvrir-go.md` : lire ce code quand on découvre Go. Chaque notion
  renvoie au fichier qui l'utilise : modules et `go.mod`, paquets, `embed`,
  interfaces (`Store`), goroutines et `context`, gestion d'erreurs, tests en
  tableaux, build tags, `ldflags`, build multi-étapes.
- `docs/conception.md` : copie de la présente spec.
- Code : commentaire de doc sur chaque fonction non triviale, à la manière Go
  (« `NomFonction` fait… »).

## 12. Plan de recette (depuis l'intranet prod)

Contrainte : 3 ressources. Avant chaque passage, vérifier la tuile quota.

**Passage 1 — PostgreSQL** (base + app + endpoint)

1. Base `canary-db`, PostgreSQL 17. Copier le `connection_string` (affiché une
   seule fois).
2. Application `canary` : `ghcr.io/tomvendee/cassiopee-canary`, tag `latest`,
   port cible **vide** (test de la détection par `EXPOSE`), env
   `DATABASE_URL=<connection_string>`, `STARTUP_DELAY=20`, `HELLO=world` ; trois
   sondes activées, chemins suggérés, port 8080.
3. Endpoint `canary` : TLS letsencrypt, WebSocket `proxyReadTimeout=60`, chemin
   `/canary`.
4. Ouvrir la page : cinq étoiles vertes attendues.
5. Une modification à la fois, dans cet ordre, effet attendu sur la page :
   - modifier `HELLO` → nouvelle valeur après redéploiement ;
   - changer de tag (`sha-…`) → version affichée ;
   - échec readiness, **une réplique** → page injoignable ~60 s puis retour ;
   - échec liveness → uptime remis à zéro, compteurs à zéro ;
   - répliques 3 → rafale : 3 pods ; échec readiness sur l'un d'eux → il
     disparaît de la rafale ~60 s ;
   - annotation `nginx.ingress.kubernetes.io/proxy-body-size: 2m` → 1 Mo OK,
     5 Mo refusé (413) ;
   - silence WebSocket 90 s → coupure vers 60 s ;
   - écriture en base → compteur +1, conservé après redémarrage du pod ;
   - utilisateur `lecteur` en `readOnly` → formulaire : connexion et lecture OK,
     écriture et DDL refusées ;
   - rotation du mot de passe principal → formulaire : ancien et nouveau mots
     de passe testés ; noter lequel passe (effet réel inconnu, cf. section 2) ;
   - suspension → étoile Base orange puis rouge ; reprise → verte, compteur
     intact ;
   - agrandissement du stockage → vérifié dans l'intranet seulement.

**Passages 2 et 3 — MariaDB, puis MongoDB** : supprimer la base, en créer une
avec l'autre moteur, remplacer `DATABASE_URL` dans l'app, rejouer la partie
base. Si la base supprimée reste comptée pendant ses 7 jours de grâce, le
passage est bloqué : le noter, c'est un résultat.

**Passage 4 — CronJob** (app + endpoint + CronJob, sans base)

- Application `canary-cron`, type CronJob, même image, `CANARY_MODE=cron`,
  `CANARY_REPORT_URL=http://canary:<port du service>`, planification
  `*/2 * * * *` → battements visibles sur la page. Le nom DNS du service n'est
  pas mesuré : le guide indique comment le vérifier si rien n'arrive.
- `CRON_EXIT_CODE=1`, `restartPolicy: OnFailure` → tentatives répétées
  visibles (même pod, plusieurs battements).

**Nettoyage** : tout supprimer, vérifier le quota à 0/3.

## 13. Hors périmètre

S3, backups, volumes persistants ; tests de l'interface de l'intranet
elle-même ; image multi-architecture ; thème clair ; exécution automatique de la
recette (elle se fait à la main, depuis l'intranet, c'est le but).

## 14. Inconnues à lever pendant la recette

Non mesurées, chacune devient un résultat de recette :

1. Nom DNS du service d'une application (pour `CANARY_REPORT_URL`).
2. Une base supprimée compte-t-elle dans le quota pendant sa période de grâce ?
3. Les permissions des utilisateurs secondaires sont-elles réellement appliquées ?
4. La rotation change-t-elle le mot de passe dans la base, ou seulement le secret ?
5. La sortie externe est-elle ouverte en prod ?
6. Les annotations d'ingress sont-elles toutes acceptées (les *snippets* sont
   souvent désactivés côté ingress-nginx) ?
7. Nom de l'issuer letsencrypt attendu en prod.
8. La détection du port par `EXPOSE` fonctionne-t-elle avec GHCR public ?
