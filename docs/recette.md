# Recette de Cassiopée en prod

Ce guide fait vérifier, depuis l'intranet de production
(`https://intranet.sigl.epita.fr`), chaque fonctionnalité de Cassiopée que
l'intranet expose : applications, endpoints, bases de données. On joue
l'étudiant : tout se fait dans les formulaires de l'intranet, et c'est la page
du canary qui dit ce qui a réellement pris effet dans le cluster.

Compter environ deux heures pour l'ensemble. Chaque passage peut se faire un
autre jour.

## Avant de commencer

- **Quota : 3 ressources par compte**, applications, bases et endpoints
  confondus. La tuile de quota de l'intranet doit montrer 0/3 avant le passage
  1. Un dépassement est refusé avec une erreur 429 qui le dit clairement.
- **L'image doit être publique** sur GHCR (voir le README). Sinon, le pod reste
  en `ImagePullBackOff` et l'application ne passe jamais à `deployed`.
- **Tout est asynchrone.** Cassiopée écrit dans Git, puis ArgoCD applique. Un
  statut met entre 40 s et un peu plus d'une minute à suivre une action
  (mesuré sur staging). Les statuts de l'intranet retardent toujours sur la
  page du canary, qui voit l'état réel.
- **Les secrets ne s'affichent qu'une fois** : le `connection_string` d'une
  base, le mot de passe d'un utilisateur ajouté, le nouveau mot de passe après
  rotation. Les coller tout de suite dans un endroit sûr.
- Noter chaque résultat dans la [grille](#grille-de-résultats) en fin de
  document : c'est le livrable de la recette.

Ce que dit chaque voyant est détaillé dans
[configuration.md](configuration.md#les-vérifications).

---

## Passage 1 — Application, endpoint, PostgreSQL

Ordre volontaire : l'application d'abord. Sur staging, un compte sans aucune
ressource ne pouvait pas créer de base (500, « namespaces is forbidden ») tant
qu'une application n'avait pas fait créer son namespace. Si la prod se
comporte pareil, cet ordre l'évite ; si la base passe en premier sans erreur,
c'est un résultat à noter.

### Étape 1 — Créer l'application

Intranet → Cassiopée → Applications → créer. Méthode de création :
**Détaillée** (la méthode Standard est testée à part, en
[variante](#variante--méthode-standard)).

| Section | Champ | Valeur |
|---|---|---|
| Configuration | Nom | `canary` |
| | Type | `Deployment` |
| | Repository de l'image | `ghcr.io/tomvendee/cassiopee-canary` |
| | Tag de l'image | `latest` |
| | Politique de pull de l'image | `Always` |
| | Répliques | `1` |
| Ports | Port du conteneur | ajouter un port : nom `http`, port `8080` |
| Service | Port du service | `80` |
| | Port cible du service | **taper `8080`**, même si le champ l'affiche déjà en grisé |
| Probes | Startup | activée, chemin `/startup`, port `8080` |
| | Readiness | activée, chemin `/ready`, port `8080` |
| | Liveness | activée, chemin `/healthz`, port `8080` |
| Ressources | CPU demandé / Mémoire demandée | `50m` / `64Mi` |
| Variables d'environnement | | `STARTUP_DELAY` = `20`, `HELLO` = `world` |

**Piège du formulaire** (lu dans le code de l'intranet, `main` du 05/10/2026) :
le `8080` grisé du port cible n'est qu'un exemple, la valeur réellement envoyée
par défaut est `80` ; et une section Ports vide envoie d'office un port de
conteneur `80`. Sans saisie explicite, le service vise le port 80 où rien
n'écoute, et l'endpoint répond 502. C'est aussi pourquoi la détection du port
par l'`EXPOSE` de l'image ne peut pas se tester depuis l'intranet : il envoie
toujours un port.

**Attendu dans l'intranet** : l'application passe à `deployed` en une à deux
minutes. Le `STARTUP_DELAY` de 20 s retient le pod : c'est la sonde startup qui
travaille.

Pour protéger la page, ajouter `CANARY_TOKEN` = un secret de votre choix :
le navigateur demandera ce mot de passe (identifiant libre).

### Étape 2 — Créer l'endpoint

Endpoints → créer.

| Section | Champ | Valeur |
|---|---|---|
| Configuration | Nom | `canary` |
| | Service cible | `canary` |
| | Port du service | `80` |
| | Host | `canary-<votre login>.cassiopee.sigl.epita.fr` |
| | Path | `/canary` |
| | Path type | `Prefix` |
| TLS | | activé, Issuer `letsencrypt` |
| WebSocket | | activé, Proxy read timeout `60`, Proxy send timeout `60` |
| Annotations | | aucune pour l'instant |

Le domaine `*.cassiopee.sigl.epita.fr` est celui que l'intranet propose (et
qu'il utilise en méthode Standard). Le login dans l'hôte évite de prendre le
nom d'un autre utilisateur. Le nom exact de l'issuer n'est pas documenté : si
le certificat n'arrive pas, c'est un résultat à noter.

Ouvrir `https://canary-<votre login>.cassiopee.sigl.epita.fr/canary/`.

**Attendu sur la page :**

| Étoile | État | Pourquoi |
|---|---|---|
| Application | verte | uid 1000, limites `CPU 200m, mémoire 256 Mio` (fixées par le formulaire) |
| Sondes | verte | trois sondes appelées par kubelet, tracés qui avancent ; startup appelée plusieurs fois pendant les 20 s de délai |
| Endpoint | verte | `X-Forwarded-For` présent, `X-Forwarded-Proto: https`, écho WebSocket reçu ; « Préfixe reçu » vaut `/canary/` |
| Base | grise | pas encore de `DATABASE_URL` |
| Réseau | verte | DNS interne et externe résolus |

La carte Réseau sortant indique aussi si HTTPS vers Internet est ouvert : c'est
un réglage de SiOps, à noter, pas une panne.

### Étape 3 — Créer la base PostgreSQL

Bases de données → créer.

| Champ | Valeur |
|---|---|
| Nom | `canary-db` |
| Moteur / Version | PostgreSQL / `17` |
| Taille du stockage | `10Gi` |
| Nom de la base initiale | `app` |
| Options avancées | laisser par défaut |

**Copier le `connection_string`** de la fenêtre qui s'ouvre. Il ressemble à
`postgresql://postgres:<mot de passe>@canary-db-postgresql:5432/app`. Il ne
sera plus jamais affiché.

**Attendu** : la base passe à `deployed` en une minute environ.

### Étape 4 — Brancher l'application sur la base

Applications → `canary` → modifier → variables d'environnement : ajouter
`DATABASE_URL` = le `connection_string` copié. Enregistrer.

**Attendu** : un nouveau pod démarre (le nom de pod change dans l'en-tête),
l'étoile Base passe au vert, la carte Base affiche le moteur, sa version et
« 1 ligne » : la ligne écrite à la connexion. La carte Variables d'env montre
`DATABASE_URL` avec le mot de passe masqué.

C'est aussi le test de la modification d'une application.

### Étape 5 — Une modification à la fois

Faire les actions dans cet ordre ; attendre l'effet avant de passer à la
suivante.

| # | Action | Effet attendu sur la page |
|---|---|---|
| 1 | Application : `HELLO` → `bonjour` | nouveau pod ; la carte Variables d'env montre `bonjour` |
| 2 | Application : tag `latest` → une version figée (`1.0.0` ou `sha-…`) | la version de l'en-tête change |
| 3 | Page : **Faire échouer readiness 60 s** (une seule réplique) | ~30 s plus tard (3 échecs × 10 s), le pod sort du service : la page affiche « Pod injoignable depuis N s ». Elle revient seule environ 60 s après le clic |
| 4 | Page : **Faire échouer liveness 60 s** | ~30 s plus tard, kubelet redémarre le conteneur : « En ligne depuis » repart de zéro, les compteurs de sondes aussi, le nom de pod ne change pas (même pod, conteneur relancé) |
| 5 | Application : répliques `3` | **Rafale de 20 requêtes** : « 3 pods vus » et la répartition. Faire échouer la readiness : le pod qui a reçu le clic disparaît de la rafale pendant environ une minute |
| 6 | Page : **Envoyer 1 / 5 / 10 Mo** | noter les résultats : c'est la limite par défaut de l'ingress (souvent 1 Mo, donc 5 et 10 Mo refusés en 413) |
| 7 | Endpoint : annotation `nginx.ingress.kubernetes.io/proxy-body-size` = `20m` | les envois de 5 et 10 Mo passent. Sinon, l'annotation n'est pas appliquée : à noter |
| 8 | Page : **Rester muet 90 s** | coupure vers 60 s : c'est le `proxyReadTimeout` |
| 9 | Endpoint : Proxy read timeout `120`, puis rejouer le silence | « tenu 90 s sans coupure » |
| 10 | Page : **Écrire une ligne** | le compteur augmente ; il survit aux redémarrages (étape 4 du tableau) |
| 11 | Base → Utilisateurs → ajouter `lecteur`, droits `readOnly` ; copier son mot de passe | carte Identifiants avec `lecteur` : connexion ✓, lecture ✓, écriture ✗, DDL ✗ |
| 12 | Même chose avec `redacteur` en `readWrite`, puis `chef` en `admin` | `readWrite` : écriture ✓ attendue ; `admin` : DDL ✓ attendue. Noter ce qui passe vraiment |
| 13 | Base → renouveler le mot de passe principal ; copier le nouveau | carte Identifiants : tester `postgres` avec l'ancien, puis avec le nouveau. Noter lequel passe (voir ci-dessous) |
| 14 | Base → suspendre | étoile Base orange (« injoignable depuis N s »), puis rouge après 60 s |
| 15 | Base → reprendre | Base verte en une minute environ, compteur de lignes intact |
| 16 | Base → stockage `10Gi` → `11Gi` | à vérifier dans le détail de la base, sur l'intranet : l'app ne voit pas le volume |

**Sur la rotation (n° 13).** Cassiopée réécrit le secret, mais son propre
message prévient qu'il faut peut-être redémarrer la base pour que le nouveau
mot de passe s'applique. Trois issues possibles, toutes utiles : l'ancien
passe encore (rotation sans effet sur la base), le nouveau passe seul
(rotation effective), aucun ne passe. Si la rotation est effective, l'étoile
Base passera au rouge « authentification refusée » au prochain redémarrage de
l'app : mettre alors le nouveau mot de passe dans `DATABASE_URL`.

---

## Passages 2 et 3 — MariaDB, puis MongoDB

1. Bases → supprimer `canary-db`. Regarder la tuile de quota : Cassiopée
   annonce une période de grâce de 7 jours pour une base supprimée. Si le
   quota reste à 3/3, la base supprimée compte encore : le noter, et
   reprendre ce passage plus tard.
2. Créer `canary-db` en **MariaDB 11**. Le `connection_string` ressemble à
   `mysql://mariadb:<mot de passe>@canary-db-mariadb:3306/app`.
3. Application → `DATABASE_URL` = ce nouveau `connection_string`.
4. Rejouer les n° 10 à 15 du tableau.
5. Recommencer avec **MongoDB 7** : `connection_string` de la forme
   `mongodb://mongodb:<mot de passe>@canary-db-mongodb:27017/app?authSource=admin`.
   Pour les utilisateurs ajoutés, le canary garde `authSource=admin` de
   `DATABASE_URL` : si un utilisateur ajouté ne peut pas se connecter alors que
   son mot de passe est bon, c'est peut-être qu'il est créé dans une autre base
   d'authentification. À noter.

**Attendu** : la carte Base affiche « MariaDB » ou « MongoDB » et sa version ;
mêmes comportements que PostgreSQL. Pour MongoDB, la DDL testée est la création
et la suppression d'une collection.

---

## Passage 4 — CronJob

Il faut une place libre : supprimer la base. L'application `canary` et son
endpoint restent, ils reçoivent les battements.

Applications → créer, méthode Détaillée :

| Champ | Valeur |
|---|---|
| Nom | `canary-cron` |
| Type | `CronJob` |
| Repository / Tag | les mêmes que `canary` |
| Planification du CronJob | `*/2 * * * *` |
| Politique de redémarrage | `OnFailure` |
| Probes | aucune |
| Variables | `CANARY_MODE` = `cron` ; `CANARY_REPORT_URL` = l'adresse suggérée par la carte CronJob de la page (de la forme `http://canary.<namespace>.svc.cluster.local:80`) ; `CANARY_TOKEN` s'il est défini sur `canary` |

**Attendu** : en moins de deux minutes, un battement apparaît dans la carte
CronJob (pod, heure, code 0).

Ensuite : `CRON_EXIT_CODE` = `1`. Avec `OnFailure`, Kubernetes relance le
conteneur en échec : plusieurs battements du même pod pour une même
exécution. Avec `Never`, chaque nouvelle tentative crée un nouveau pod. Noter
ce qui se passe.

Si rien n'arrive : essayer `http://canary:80` (nom court du service), puis
vérifier le namespace et le nom de déploiement affichés par la carte. Le nom
exact du service qu'ArgoCD crée n'a pas été mesuré.

---

## Variante — méthode Standard

À faire avec une place libre, par exemple à la fin. Créer une application en
méthode **Standard** : l'intranet crée en plus un endpoint automatique, d'hôte
`<nom>.cassiopee.sigl.epita.fr`, TLS désactivé. Ouvrir
`http://<nom>.cassiopee.sigl.epita.fr/`.

**Attendu, d'après le code de l'intranet** : un 502. La méthode Standard
envoie le port de conteneur et le port cible `80`, sans section pour les
changer, alors que le canary écoute sur 8080. La méthode Standard ne sert donc
qu'une image qui écoute sur 80. Si la page s'affiche malgré tout, noter le
résultat : la vérification HTTPS doit alors être orange (« requête arrivée en
http ») et les sondes grises, la méthode Standard ne les activant pas.

---

## Nettoyage

Supprimer l'application CronJob, l'application `canary`, **puis son
endpoint** : supprimer une application ne supprime pas ses endpoints (mesuré
sur staging). Supprimer la base si elle existe encore. La tuile de quota doit
revenir à 0/3, ou à ce qu'occupe encore une base en période de grâce.

---

## En cas de problème

| Symptôme | Cause probable | Où regarder |
|---|---|---|
| L'application ne passe jamais à `deployed` | Paquet GHCR privé (`ImagePullBackOff`) | `docker logout ghcr.io && docker pull ghcr.io/tomvendee/cassiopee-canary:latest` doit réussir |
| Page en 503 dès le départ | Readiness en échec, ou mauvais port de sonde | Port des sondes = `8080` ; attendre la fin du `STARTUP_DELAY` |
| Page en 502 ou 504 | Port cible du service resté à 80 (valeur par défaut cachée du formulaire), port d'écoute hors NetworkPolicy, ou port du service faux | Port cible et port du conteneur = `8080`, saisis à la main ; port du service de l'endpoint = port du service de l'app |
| Page en 404 | Path de l'endpoint ou hôte différent de l'URL ouverte | Ouvrir `https://<host><path>/` exactement |
| Certificat invalide | Issuer inconnu ou certificat pas encore émis | Attendre quelques minutes, puis noter le nom d'issuer essayé |
| 429 à la création | Quota atteint | Tuile de quota ; une base supprimée compte peut-être encore 7 jours |
| 500 « Forbidden » à la création d'une base | Namespace pas encore créé (compte sans ressource) | Créer d'abord une application, attendre `deployed` |
| Base orange dès le départ | `DATABASE_URL` mal copiée, ou base pas encore `deployed` | Carte Base, ligne Erreur ; hôte attendu `<nom>-<moteur>` |
| Base rouge « authentification refusée » | Le mot de passe a changé (rotation effective) | Mettre le nouveau mot de passe dans `DATABASE_URL` |
| Étoile Endpoint orange | Arrivée en HTTP | TLS de l'endpoint ; ouvrir l'URL en `https://` |
| Écho WebSocket en échec | Option WebSocket de l'endpoint désactivée | Endpoint → WebSocket activé |
| Aucun battement de cron | `CANARY_REPORT_URL` faux, ou jeton manquant | Carte CronJob : adresse suggérée ; même `CANARY_TOKEN` des deux côtés |

La page a toujours raison sur ce qui tourne ; l'intranet a raison sur ce qui
a été demandé. Un écart entre les deux est un résultat de recette.

---

## Grille de résultats

À remplir pendant la recette. Les huit premières lignes sont les questions
que la lecture du code de SiOps n'a pas pu trancher.

| # | Question | Résultat | Date |
|---|---|---|---|
| 1 | Quelle adresse de service fonctionne pour `CANARY_REPORT_URL` ? | | |
| 2 | Une base supprimée compte-t-elle dans le quota pendant sa période de grâce ? | | |
| 3 | Les droits `readOnly`, `readWrite`, `admin` sont-ils appliqués ? (lecture / écriture / DDL pour chacun) | | |
| 4 | La rotation change-t-elle le mot de passe dans la base, ou seulement le secret ? | | |
| 5 | La sortie HTTPS vers Internet est-elle ouverte en prod ? | | |
| 6 | Les annotations d'endpoint sont-elles appliquées (`proxy-body-size`) ? | | |
| 7 | Quel issuer TLS donne un certificat valide ? | | |
| 8 | Le port est-il détecté depuis l'`EXPOSE` de l'image ? | Non testable depuis l'intranet : il envoie toujours un port (80 par défaut) | 05/10/2026 |
| 9 | Les trois sondes sont-elles appelées au rythme configuré ? | | |
| 10 | La readiness en échec retire-t-elle le pod du service ? La liveness redémarre-t-elle le conteneur ? | | |
| 11 | Trois répliques sont-elles servies à tour de rôle ? | | |
| 12 | `proxyReadTimeout` coupe-t-il un WebSocket inactif au délai réglé ? | | |
| 13 | Suspendre puis reprendre une base conserve-t-il ses données ? | | |
| 14 | MariaDB et MongoDB se comportent-ils comme PostgreSQL ? | | |
| 15 | Le CronJob s'exécute-t-il à l'heure, et `restartPolicy` est-elle respectée ? | | |
| 16 | La méthode Standard crée-t-elle un endpoint joignable ? | | |
