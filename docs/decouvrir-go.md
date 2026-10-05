# Découvrir Go avec ce code

Ce guide suit le code du canary pour présenter Go à quelqu'un qui connaît déjà
un autre langage (TypeScript, Java…). Chaque notion renvoie au fichier où on
la voit à l'œuvre : lire le guide avec le fichier ouvert à côté.

## Les commandes du quotidien

| Commande | Effet |
|---|---|
| `go run .` | Compile et lance le paquet du dossier courant |
| `go build -o canary .` | Produit le binaire `canary` |
| `go test ./...` | Lance tous les tests ; `-race` détecte les accès concurrents non protégés, `-run Nom` filtre, `-v` détaille |
| `go vet ./...` | Repère les erreurs probables que le compilateur laisse passer |
| `gofmt -l .` | Liste les fichiers mal formatés ; `gofmt -w .` les corrige. En Go, le format n'est pas un débat : tout le monde utilise gofmt |
| `go doc net/http ServeMux` | Documentation d'un symbole, à la version exacte utilisée par le projet |
| `go mod tidy` | Ajoute les dépendances manquantes, retire les inutiles |

## Modules et dépendances — `go.mod`, `go.sum`

`go.mod` déclare le nom du module (`github.com/tomvendee/cassiopee-canary`),
la version de Go, et les dépendances avec leur version exacte. `go.sum` contient
leurs empreintes : un téléchargement altéré est refusé. Les deux fichiers se
commitent. `go get github.com/coder/websocket@v1.8.15` ajoute une dépendance ;
les blocs `// indirect` sont les dépendances de nos dépendances.

## Un paquet réparti sur plusieurs fichiers — tous les `.go`

Tous les fichiers commencent par `package main` : ils forment un seul paquet,
et chacun voit tout ce que les autres déclarent, sans import entre eux.
`server.go` appelle `NewProbes` défini dans `probes.go` comme s'il était à
côté. Le découpage en fichiers sert la lecture, pas la visibilité.

La majuscule a un sens : `NewServer` est exporté (visible depuis un autre
paquet), `servePage` ne l'est pas. Ici tout est dans `main`, mais la
convention aide à repérer ce qui forme l'interface d'un fichier.

## Structs et méthodes — `probes.go`, `config.go`

Pas de classes : des `struct` et des fonctions attachées par un **récepteur**.

```go
func (p *Probes) Hit(kind ProbeKind, fromKubelet bool) bool { … }   // probes.go
func (t DBTarget) WithCredentials(user, password string) DBTarget { … } // config.go
```

`*Probes` est un récepteur pointeur : `Hit` modifie les compteurs de l'objet
d'origine. `DBTarget` est un récepteur valeur : `WithCredentials` travaille sur
une copie et la renvoie, l'original reste intact. C'est exactement ce qu'on
veut pour tester un autre utilisateur sans toucher à la cible principale.

Le constructeur est une simple fonction, `NewProbes`, par convention.

## Les valeurs nulles — partout

Une variable non initialisée vaut la « valeur nulle » de son type : `0`, `""`,
`false`, `nil`, une `time.Time` à l'an 1. C'est pourquoi `Config{}` est
utilisable tel quel, et pourquoi la page traite une date antérieure à l'an
2000 comme « jamais ».

Piège rencontré en écrivant `config_test.go` : `==` ne compare pas une struct
qui contient une map (`DBTarget.Query`). Le compilateur refuse ; on passe par
`reflect.DeepEqual`.

## Interfaces implicites — `db.go`, `egress.go`

```go
type Store interface {
    Ping(ctx context.Context) (version string, err error)
    Write(ctx context.Context, pod string) error
    …
}
```

Aucun type n'écrit « implements Store ». `sqlStore` (`db_sql.go`) et
`mongoStore` (`db_mongo.go`) ont ces méthodes : ils satisfont l'interface, le
compilateur le vérifie là où on les utilise comme `Store`. Les tests en
profitent : `fakeStore` (`db_test.go`) est une base en mémoire qui satisfait
la même interface, sans aucun framework de mock.

Même idée dans `egress.go` : l'interface `Resolver` ne demande que
`LookupHost`. `net.DefaultResolver` la satisfait en production, un faux
résolveur dans les tests.

Règle de Go : petite interface, déclarée par celui qui l'utilise.

## Plusieurs valeurs de retour et erreurs — `config.go`, `db_postgres.go`

Une fonction qui peut échouer renvoie sa valeur **et** une erreur :

```go
cfg, err := Load(os.Getenv)
if err != nil {
    fmt.Fprintln(os.Stderr, "configuration invalide :", err)
    os.Exit(2)
}
```

Pas d'exceptions : l'erreur est une valeur, et le `if err != nil` est voulu.
On l'enrichit en l'enveloppant : `fmt.Errorf("DATABASE_URL : %w", err)`
(`config.go`). On l'inspecte ensuite :

- `errors.Is(err, ErrNotFailable)` : est-ce cette erreur-là ? (`probes_test.go`)
- `errors.As(err, &pgErr)` : y a-t-il, dans la chaîne, une erreur de ce type ?
  C'est ainsi que `db_postgres.go` reconnaît un mot de passe refusé (code
  PostgreSQL `28P01`) même enveloppé par plusieurs couches du pilote.

Les retours peuvent être nommés, ce qui documente leur sens :
`Count(ctx) (rows int64, last time.Time, err error)`.

## Fonctions comme valeurs — `config.go`, `server.go`

```go
func Load(getenv func(string) string) (Config, error)
```

`Load` reçoit la fonction qui lit l'environnement : `os.Getenv` en production,
une fonction qui lit une table dans les tests. Même principe pour
`Deps.Now func() time.Time`, l'horloge, que les tests remplacent par une
horloge qu'ils avancent à la main (`fakeClock`, `probes_test.go`).

`probeHandler` (`server.go`) renvoie une **fermeture** : une fonction qui garde
accès aux variables `p` et `kind` de l'appel qui l'a créée.

## Goroutines, `select` et `context` — `main.go`, `db.go`

```go
go func() { errc <- srv.ListenAndServe() }()   // main.go

select {
case err := <-errc:   // le serveur s'est arrêté seul
case <-ctx.Done():    // SIGTERM reçu
}
```

`go f()` lance `f` dans une goroutine, un fil d'exécution très léger (on peut
en avoir des milliers). `errc` est un **canal** : un tuyau typé entre
goroutines. `select` attend le premier canal prêt.

`ctx` est un `context.Context` : il porte une annulation et une échéance.
`signal.NotifyContext` l'annule à la réception de SIGTERM, ce que Kubernetes
envoie avant d'arrêter un pod. Toutes les boucles de fond (`Monitor.Run`,
`EgressWatcher.Run`) surveillent `ctx.Done()` et s'arrêtent avec lui.
`context.WithTimeout(ctx, StepTimeout)` (`db.go`) borne chaque appel à la base
à 5 s : une base suspendue ne bloque jamais rien.

`net/http` lance lui-même une goroutine par requête : le code d'une route peut
s'exécuter plusieurs fois en même temps.

## Protéger les données partagées — `probes.go`

Kubelet frappe les sondes pendant que la page lit leur état : deux goroutines
touchent les mêmes compteurs. Un `sync.Mutex` les protège :

```go
p.mu.Lock()
defer p.mu.Unlock()
```

`defer` exécute l'appel à la sortie de la fonction, quel que soit le chemin de
sortie : impossible d'oublier de déverrouiller. `go test -race` vérifie qu'aucun
accès n'échappe au verrou ; `TestProbesConcurrent` lance 50 goroutines pour le
prouver.

## Le serveur HTTP de la bibliothèque standard — `server.go`

Pas de framework : `net/http` suffit. Depuis Go 1.22, les motifs de routes
comprennent la méthode et des paramètres :

```go
mux.HandleFunc("GET /api/status", …)
mux.HandleFunc("POST /api/probes/{kind}", func(w http.ResponseWriter, r *http.Request) {
    kind := r.PathValue("kind")
    …
})
```

Une requête `GET` sur une route seulement déclarée en `POST` reçoit 405 sans
code de notre part. Un **middleware** est une fonction qui prend un
`http.Handler` et en renvoie un autre (`stripPrefix`, `requireToken`).

## Embarquer des fichiers — `server.go`

```go
//go:embed web/index.html
var page []byte
```

La directive `//go:embed` copie le fichier dans le binaire à la compilation.
L'image Docker n'a besoin de rien d'autre que le binaire.

## Import pour effet de bord — `db_postgres.go`

```go
_ "github.com/jackc/pgx/v5/stdlib" // enregistre le pilote "pgx"
```

Le `_` importe un paquet sans utiliser ses noms : seule sa fonction `init`
s'exécute, et elle enregistre le pilote auprès de `database/sql`. Ensuite,
`sql.Open("pgx", …)` le trouve par son nom.

## JSON et étiquettes de champs — `status.go`, `db.go`

```go
type ProbeState struct {
    Hits    int       `json:"hits"`
    LastHit time.Time `json:"lastHit"`
}
```

L'étiquette entre accents graves dit à `encoding/json` quel nom utiliser. Un
pointeur `nil` devient `null` (`Status.DB` sans base), une `time.Time` une date
ISO 8601.

## Les tests — tous les `*_test.go`

- Un test est une fonction `TestXxx(t *testing.T)` dans un fichier
  `_test.go`. Pas de bibliothèque d'assertions : un `if` et `t.Errorf`.
- **Tests en tableaux** : une liste de cas, une boucle, un `t.Run(nom, …)` par
  cas (`TestParseDatabaseURL`, `TestEvaluateRules`). Ajouter un cas, c'est
  ajouter une ligne.
- `httptest.NewRecorder` appelle un handler sans ouvrir de port
  (`server_test.go`) ; `httptest.NewServer` démarre un vrai serveur local
  (`ws_test.go`, `cron_test.go`).
- `t.Helper()` fait pointer l'erreur vers l'appelant, `t.TempDir()` donne un
  dossier supprimé à la fin, `t.Cleanup` enregistre un nettoyage.
- **Build tags** : `//go:build integration` en tête de
  `db_integration_test.go` exclut ce fichier, sauf avec
  `go test -tags integration`. Les tests rapides restent rapides.

## Injecter la version à la compilation — `main.go`, `Dockerfile`

```go
var version = "dev"
```

```bash
go build -ldflags "-X main.version=1.2.3" .
```

`-X` remplace la valeur d'une variable de type chaîne au moment de l'édition
de liens. La CI y met le tag ou le commit, et la page l'affiche.

## Un binaire statique dans une image minuscule — `Dockerfile`

`CGO_ENABLED=0` interdit tout appel à du code C : le binaire ne dépend
d'aucune bibliothèque système et tourne dans `distroless/static`, une image
sans shell. Le build multi-étapes compile dans l'image `golang` (plus de
800 Mo) et ne garde que le binaire dans l'image finale (environ 17 Mo).

## Pour s'exercer sur ce code

1. Ajouter une variable `CANARY_TITLE` qui remplace le titre de la page :
   un champ dans `Config`, un cas dans `TestLoadErrorsAndWarnings`, un champ
   dans `Status`, une ligne dans `renderHeader`.
2. Ajouter une vérification « Répliques » qui passe à l'orange si la rafale ne
   voit qu'un pod alors que l'utilisateur en attend plusieurs. Où la
   calculer : serveur ou page ? (Indice : qui voit plusieurs pods ?)
3. Lire `Monitor.Tick` (`db.go`) et expliquer pourquoi les appels à la base se
   font hors du verrou.

## Pour aller plus loin

- [Le tour de Go](https://go.dev/tour/) : les bases, dans le navigateur.
- [Effective Go](https://go.dev/doc/effective_go) : le style et les idiomes.
- [Go by Example](https://gobyexample.com/) : une notion, un exemple court.
- [Gérer les dépendances](https://go.dev/doc/modules/managing-dependencies).
- [Le routage de `net/http` depuis Go 1.22](https://go.dev/blog/routing-enhancements).
