# Cassiopée canary

Une application témoin à déployer **avec Cassiopée, depuis l'intranet SIGL**,
exactement comme le ferait un étudiant. Une fois en ligne, sa page montre,
voyant par voyant, que chaque fonctionnalité de Cassiopée a réellement pris
effet dans le cluster : variables d'env, ports, répliques, sondes, limites,
securityContext, endpoint (TLS, chemin, WebSocket, annotations), bases de
données (PostgreSQL, MariaDB, MongoDB) et CronJob.

![La page du canary : la constellation de Cassiopée, une étoile par domaine, et les cartes de vérification](docs/capture.png)

Chaque étoile du W de Cassiopée est un domaine testé. Elle s'allume en vert
quand toutes ses vérifications passent, en orange ou en rouge sinon, et reste
grise tant que rien n'est configuré.

## Déployer depuis l'intranet

L'essentiel, avant le pas à pas complet de [docs/recette.md](docs/recette.md) :

| Champ de l'intranet | Valeur |
|---|---|
| Repository de l'image | `ghcr.io/tomvendee/cassiopee-canary` |
| Tag de l'image | `latest`, ou une version figée (`1.0.0`, `sha-abc1234`) |
| Port du conteneur | `8080` — seuls 80, 8080, 443 et 8443 passent la NetworkPolicy de Cassiopée |
| Sondes | `/startup`, `/ready`, `/healthz` sur le port 8080 : les valeurs suggérées par le formulaire |
| `DATABASE_URL` | le `connection_string` affiché **une seule fois** à la création de la base |

Toutes les variables reconnues sont décrites dans
[docs/configuration.md](docs/configuration.md).

## Lancer en local

Prérequis : Go 1.27 et Docker.

```bash
docker compose up -d --wait postgres
DATABASE_URL=postgresql://postgres:canary@localhost:15432/app go run .
```

La page est sur <http://localhost:8080/>. En local, les étoiles Sondes et
Endpoint restent grises : il n'y a ni kubelet ni ingress pour les allumer.

Pour faire tourner l'image elle-même, avec les limites qu'impose l'intranet
(200m de CPU, 256 Mio) :

```bash
docker compose up --build canary
```

Les ports locaux des bases sont décalés (15432, 13306, 37017) pour ne pas
gêner une base déjà installée sur la machine.

## Tests

```bash
go test -race ./...
```

Les tests d'intégration tournent contre de vraies bases :

```bash
docker compose up -d --wait postgres mariadb mongo
CANARY_TEST_POSTGRES_URL=postgresql://postgres:canary@localhost:15432/app \
CANARY_TEST_MARIADB_URL=mysql://root:canary@localhost:13306/app \
CANARY_TEST_MONGODB_URL='mongodb://root:canary@localhost:37017/app?authSource=admin' \
go test -race -tags integration ./...
```

Avant de commiter : `gofmt -l .` doit être vide et `go vet ./...` muet. La CI
vérifie les trois.

## Publier une version

La CI (`.github/workflows/build.yml`) construit et pousse l'image sur GHCR :

- un push sur `main` publie `latest` et `sha-<commit court>` ;
- un tag `v1.2.3` publie `1.2.3` :

```bash
git tag v1.0.0 && git push origin v1.0.0
```

La version publiée s'affiche dans l'en-tête de la page : après un changement
de tag dans l'intranet, on voit tout de suite si le nouveau pod tourne bien la
nouvelle image.

### Le paquet doit rester public

Cassiopée n'a aucun champ pour un secret de registre : un paquet privé donne
un pod bloqué en `ImagePullBackOff`. Publié par la CI de ce dépôt public, le
paquet est **public d'office** (constaté le 5 octobre 2026 à la première
publication). Pour le vérifier, sans être connecté au registre :

```bash
docker logout ghcr.io
docker pull ghcr.io/tomvendee/cassiopee-canary:latest
```

Si le pull est refusé (dans un fork, par exemple), passer le paquet en public :
GitHub → photo de profil → **Your profile** → onglet **Packages** →
`cassiopee-canary` → **Package settings** → **Danger Zone** →
**Change visibility** → **Public**.

## Documentation

| Document | Pour quoi faire |
|---|---|
| [docs/recette.md](docs/recette.md) | Tester Cassiopée en prod, pas à pas, champ par champ, avec le résultat attendu à chaque étape |
| [docs/configuration.md](docs/configuration.md) | Référence : variables d'env, routes HTTP, contenu de `/api/status` |
| [docs/architecture.md](docs/architecture.md) | Comment le code est organisé, et pourquoi |
| [docs/decouvrir-go.md](docs/decouvrir-go.md) | Lire ce code quand on découvre Go |
| [docs/conception.md](docs/conception.md) | La conception d'origine, validée avant d'écrire le code |
