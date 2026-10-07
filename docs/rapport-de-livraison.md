# tasks-mcp : rapport de livraison

Destinataire : l'intégrateur (déploiement dans ZeroClaw sur le VPS Linux). Version décrite : `v0.1.0`, dépôt `thewhitewizard/tasks-mcp`. Le détail de chaque outil est dans `README.md` ; ce rapport donne ce qu'il faut pour déployer, déclarer et approuver.

## 1. Compilation et binaire

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=v0.1.0" -o tasks-mcp .
```

Binaire statique `tasks-mcp` (environ 10 Mo), sans dépendance système. Une seule exception : la **base des fuseaux horaires n'est pas embarquée** (voir le point 3).

## 2. Les huit outils

Aucun outil n'accepte un chemin de fichier. `id` d'une tâche : `t_` + 6 caractères (`a-z2-7`). `id` d'un projet : `p_` + 6 caractères. Les ids sont acceptés en majuscules et avec des espaces autour. Dates : `due` est un jour `AAAA-MM-JJ` ; les autres horodatages sont en ISO 8601 avec décalage, à la seconde (`2026-07-14T09:30:00+02:00`). Un argument facultatif à `null` vaut « absent » ; un argument d'un autre type que celui attendu est une erreur. Toute réponse est un seul bloc texte contenant du JSON compact ; une erreur métier est un résultat avec `isError: true` et un message en anglais.

| Outil | Classe | `readOnlyHint` | `destructiveHint` | Paramètres (obligatoires en gras) | Réponse |
|---|---|---|---|---|---|
| `list_projects` | lecture | true | false | aucun | `{"projects":[{id,name,description?,created_at}]}` |
| `list_tasks` | lecture | true | false | `project?`, `status?` (`todo`/`doing`/`done`), `due_before?` (`AAAA-MM-JJ`, inclus), `tag?`, `include_done?` (booléen) | `{"tasks":[…],"truncated":true}` (`truncated` absent quand faux) ; tâches **sans `notes`** |
| `get_task` | lecture | true | false | **`id`** | `{"task":{…}}` complet, `notes` comprises |
| `create_project` | écriture | false | false | **`name`**, `description?` | `{"project":{…}}` |
| `add_task` | écriture | false | false | **`title`**, `notes?`, `project?`, `due?`, `priority?` (`low`/`normal`/`high`), `tags?` (liste de textes) | `{"task":{…}}` |
| `update_task` | écriture | false | false | **`id`**, `title?`, `notes?`, `status?`, `priority?`, `due?`, `project?`, `tags?` | `{"task":{…}}` |
| `complete_task` | écriture | false | false | **`id`** | `{"task":{…}}` |
| `delete_task` | **destructeur** | false | **true** | **`id`** | `{"deleted":{…la tâche supprimée…}}` |

Les huit déclarent aussi `openWorldHint: false` (explicite). Forme d'une tâche : `id`, `title`, `notes?`, `status`, `priority`, `due?`, `project?`, `tags` (toujours une liste, `[]` si vide), `created_at`, `updated_at`, `completed_at?`. Forme d'un projet : `id`, `name`, `description?`, `created_at`. Les champs marqués `?` sont omis quand ils sont vides.

Règles à connaître pour `update_task` : champ absent ou `null` = inchangé ; chaîne vide = effacer `notes`, `due` ou `project` ; `tags` remplace la liste (`[]` l'efface) ; `title`, `status` et `priority` ne s'effacent pas (erreur) ; `status: done` renseigne `completed_at`, tout autre statut l'efface ; aucun champ fourni = erreur `nothing to update`. `complete_task` est répétable sans effet. Exemple de réponse d'`add_task` :

```json
{"task":{"id":"t_eeymlm","title":"Pay rent","status":"todo","priority":"high","due":"2026-07-18","tags":[],"created_at":"2026-10-07T17:02:34+02:00","updated_at":"2026-10-07T17:02:34+02:00"}}
```

Le nom vu par le modèle peut être préfixé par l'agent (par exemple avec le nom du serveur déclaré) : à vérifier dans ZeroClaw avant d'écrire les règles d'approbation.

## 3. Approbation recommandée, par outil

| Outil | Recommandation | Pourquoi |
|---|---|---|
| `list_projects`, `list_tasks`, `get_task` | automatique | lecture seule |
| `create_project`, `add_task` | automatique | ajoutent seulement ; la réponse décrit ce qui a été créé |
| `complete_task` | automatique | réversible par `update_task` (`status: todo`), répétable sans effet |
| `update_task` | automatique, à durcir si l'on constate des dérives | renvoie la tâche entière pour confirmation ; mais efface `notes`, `due`, `project` ou `tags` sans possibilité d'annuler |
| `delete_task` | **toujours demandée** | seule opération irréversible |

## 4. Chemins, droits et conteneur

- **Configuration** (lecture seule suffit) : `--config /chemin/config.json` ou `TASKS_MCP_CONFIG`. Le flag l'emporte. Modèle : `config.example.json`. Exemple de contenu :

  ```json
  {"data_file": "/data/tasks-mcp/tasks.json", "timezone": "Europe/Paris"}
  ```

  Autres champs facultatifs : `max_tasks` (5000), `max_results` (50), `lock_timeout_seconds` (5, doit rester sous 30). Champ inconnu = refus de démarrer.
- **Binaire et configuration** peuvent vivre dans le dossier `mcp` monté en **lecture seule**.
- **Données** : `data_file` est un chemin **absolu hors du dossier `mcp`**, sur un volume **inscriptible** par l'utilisateur du conteneur (uid 1000). C'est le **dossier** qui doit être inscriptible, pas seulement le fichier : le serveur y crée un fichier temporaire à chaque écriture, le verrou `tasks.json.lock` et sa garde `tasks.json.lock.break`. Le dossier est créé s'il manque (droits 0700) si son parent est inscriptible ; sur un volume monté, créer le point de montage à l'avance et en donner la propriété à l'uid 1000. Le fichier de données est créé au premier ajout (droits 0600).
- Au démarrage, si la configuration est invalide ou si le dossier n'est pas inscriptible, le serveur écrit la raison sur stderr et sort avec le code 1 (2 pour une erreur d'usage). Les logs vont sur stderr ; stdout ne porte que le protocole MCP.
- **Fuseaux horaires** : le binaire a besoin de la base `zoneinfo` pour `timezone`. Dans une image sans `/usr/share/zoneinfo`, copier ce dossier ou fournir `ZONEINFO=/chemin/zoneinfo.zip`. Sans cela, le serveur refuse de démarrer avec une erreur sur `timezone`.
- Le serveur **s'arrête dès que stdin se ferme** ; une opération en cours peut alors être interrompue (l'écriture atomique protège le fichier de données). L'agent doit donc garder son entrée ouverte, ce que fait tout client MCP.
- Pas de réseau, pas de télémétrie.

## 5. Test manuel en stdio

La commande ci-dessous a été exécutée telle quelle sur le binaire. Le `sleep` garde stdin ouvert ; les réponses se repèrent par leur `id`.

```sh
{ printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_project","arguments":{"name":"Home"}}}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"add_task","arguments":{"title":"Pay rent","due":"2026-07-18","priority":"high"}}}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}'
  sleep 2; } | ./tasks-mcp --config config.json
```

À utiliser avec un `data_file` de test. Résultat attendu : la liste des huit outils (id 2), le projet créé (id 3), la tâche créée (id 4), puis la liste d'une tâche (id 5).

## 6. Ce qui a été vérifié, et ce qui ne l'a pas été

- Tests Go et CI à chaque PR : build, `go vet`, `golangci-lint` v2.14.0, `go test -race -shuffle` sous **Linux**, couverture des lignes modifiées (≥ 80 %), taille des PR (≤ 500 lignes Go). Ils couvrent les huit outils par le protocole (`HandleMessage`), la concurrence entre goroutines **et entre processus** (3 processus écrivant le même fichier, aucune écriture perdue), les fichiers corrompus et le verrou périmé.
- Exécution du **binaire réel** (Windows) par le test manuel ci-dessus et par un parcours complet : poignée de main, huit outils listés, création, mise à jour, complétion, suppression, erreurs (date impossible, id inconnu), fichier corrompu laissé intact avec message clair.
- **Non exécuté** : le binaire Linux n'a été que compilé (`GOOS=linux`), pas lancé, sur le poste de développement ; il n'a pas été essayé dans ZeroClaw ni dans le conteneur cible (uid 1000, volume monté). C'est la première vérification à faire.
- Mesure (Windows, 5000 tâches avec notes de 100 caractères) : fichier de 2,3 Mo, lecture complète environ 16 ms, écriture environ 30 ms.

## 7. Choix faits

- **Interface `Store`** (CRUD par entité, `UpdateTask(id, fn)`, `DeleteTask` qui renvoie la tâche supprimée) comme seule frontière avec le stockage ; `jsonStore` en est l'implémentation.
- **Verrou `.lock` portable** (`O_EXCL`, périmé après 30 s, garde `.lock.break`) plutôt que `flock`, pour compiler et tourner pareil sous Windows et Linux sans dépendance : voir `docs/adr/0001-portable-lock-file.md`.
- Un fichier illisible n'est **jamais** réécrit ; une écriture est atomique (temporaire, `fsync`, `rename`).
- Peu d'outils, descriptions écrites pour un petit modèle (formats, exemples). `list_tasks` ne renvoie pas les `notes` (gain de jetons) : `get_task` les donne. `add_task` n'a pas de `status` : une tâche naît `todo`.
- Les erreurs ne répètent jamais la saisie, ne donnent jamais le chemin du fichier, et aucun titre ni note n'est écrit dans les logs.

## 8. Limites connues

- Un seul utilisateur, sans authentification ; pas de sous-tâches, récurrence, pièces jointes ni heure d'échéance.
- **Pas de recherche par texte** dans `list_tasks`, et pas de page suivante au-delà de `max_results` (`truncated` invite à resserrer les filtres) : un assistant qui cherche « la tâche du dentiste » doit filtrer par projet, tag ou échéance, ou lister puis lire.
- Projets figés : ni renommage, ni archivage, ni suppression.
- Verrou fondé sur l'âge d'un fichier : un saut d'horloge, ou un processus bloqué plus de 30 s au milieu d'une écriture, peut faire se chevaucher deux écritures ; après un arrêt brutal, les écritures attendent jusqu'à 30 s puis reprennent.
- Le fichier entier est lu et réécrit à chaque opération.
- Sous Windows, un lecteur tenant le fichier ouvert peut faire échouer le `rename` final ; la cible est Linux.
- Le binaire n'embarque pas les fuseaux horaires (point 4).

## 9. Pour passer à une base de données, une API et une interface (non codé)

- Remplacer `jsonStore` par une implémentation SQL de `Store` : le verrou, le fichier temporaire, `max_tasks` à la main et le tirage d'id avec nouvelles tentatives disparaissent ; `UpdateTask(id, fn)` devient une transaction. Les outils ne changent pas.
- Déplacer `model.go` (validation, `TaskUpdate.apply`, `setStatus`) et `store.go` dans un paquet `internal/` pour les partager avec une API HTTP.
- Pour une interface : filtrage et pagination côté base (`ListTasks` renvoie tout aujourd'hui), recherche par texte, gestion des projets, et un identifiant d'utilisateur sur chaque entité pour du multi-utilisateur.
- Import : `tasks.json` porte `schema_version` ; la lecture stricte de `jsonStore.load` sert de base à un import.
