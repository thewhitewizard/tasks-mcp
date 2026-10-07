# tasks-mcp : rapport de livraison

Destinataire : l'intégrateur (déploiement dans ZeroClaw sur le VPS Linux). Version décrite : `v0.1.0` (étiquette de compilation passée par `-ldflags`, aucune étiquette git n'existe encore), dépôt `thewhitewizard/tasks-mcp`. Le détail de chaque outil est dans `README.md` ; ce rapport donne ce qu'il faut pour préparer, déclarer, approuver et vérifier.

## 1. Compilation et binaire

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=v0.1.0" -o tasks-mcp .
```

Binaire statique `tasks-mcp` (environ 10 Mo), à compiler avec Go 1.27 ou plus. Il n'a aucune dépendance système, à une exception près : la **base des fuseaux horaires n'est pas embarquée** par défaut (point 4). Il n'y a pas d'option `--version` : la version n'apparaît que dans la première ligne de stderr au démarrage (`dev` si l'on compile sans `-ldflags`).

## 2. Les huit outils

Aucun outil n'accepte un chemin de fichier. `id` d'une tâche : `t_` + 6 caractères (`a-z2-7`). `id` d'un projet : `p_` + 6 caractères. Les ids sont acceptés en majuscules et avec des espaces autour. Dates : `due` est un jour `AAAA-MM-JJ` ; les autres horodatages sont en ISO 8601 avec décalage, à la seconde (`2026-07-14T09:30:00+02:00`). Un argument facultatif à `null` vaut « absent » ; un argument d'un autre type que celui attendu est une erreur, sauf `include_done`, qui accepte aussi `"true"` et `"false"` en texte et vaut `false` pour toute autre valeur. Toute réponse est un seul bloc texte contenant du JSON compact ; une erreur métier est un résultat avec `isError: true` et un message en anglais.

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
{"task":{"id":"t_eeymlm","title":"Pay rent","status":"todo","priority":"high","due":"2027-01-15","tags":[],"created_at":"2026-10-07T17:02:34+02:00","updated_at":"2026-10-07T17:02:34+02:00"}}
```

**Nom des outils vu par le modèle.** Le serveur déclare les huit noms ci-dessus ; selon la version de ZeroClaw, l'agent peut les préfixer (par exemple `tasks__add_task`). Relever les noms réels (liste d'outils de l'agent ou logs de démarrage) avant d'écrire la politique d'approbation, et la rédiger sur les **huit noms exacts**, sans joker : une règle `tasks*` ou un préfixe mal deviné ne doit jamais approuver `delete_task` par défaut. Préférer « tout est demandé sauf ce qui est listé en automatique ».

## 3. Approbation recommandée, par outil

L'approbation est appliquée par l'**agent**, pas par le serveur : les indices `readOnlyHint` et `destructiveHint` sont des informations que l'agent peut utiliser, rien de plus.

| Outil | Recommandation | Pourquoi |
|---|---|---|
| `list_projects`, `list_tasks`, `get_task` | automatique | lecture seule |
| `create_project`, `add_task` | automatique | ajoutent seulement ; la réponse décrit ce qui a été créé |
| `complete_task` | automatique | réversible par `update_task` (`status: todo`), répétable sans effet |
| `update_task` | **demandée au début**, à passer en automatique après quelques jours d'usage observé | efface `notes`, `due`, `project` ou `tags` sans possibilité d'annuler ; un petit modèle peut se tromper de tâche |
| `delete_task` | **toujours demandée** | seule opération irréversible |

## 4. Préparation, chemins et droits

**Avant le premier démarrage**, sur l'hôte ou dans l'image :

```sh
chmod 755 /mcp/tasks-mcp                   # exécutable ; le montage ne doit pas être noexec
chmod 644 /mcp/config.json                 # lisible par l'uid 1000 ; lecture seule suffit
mkdir -p /data/tasks-mcp && chown 1000:1000 /data/tasks-mcp && chmod 700 /data/tasks-mcp
```

- **Configuration** : `--config /chemin/config.json` ou `TASKS_MCP_CONFIG` (le flag l'emporte). Modèle : `config.example.json`. Contenu minimal :

  ```json
  {"data_file": "/data/tasks-mcp/tasks.json", "timezone": "Europe/Paris"}
  ```

  Facultatifs : `max_tasks` (5000), `max_results` (50), `lock_timeout_seconds` (5, entre 1 et 29). Champ inconnu = refus de démarrer. Les chemins de cet exemple (`/mcp`, `/data/tasks-mcp`) sont des exemples : `config.example.json` en propose d'autres.
- **Binaire et configuration** peuvent vivre dans le dossier `mcp` monté en **lecture seule**.
- **Données** : `data_file` est un chemin **absolu hors du dossier `mcp`**, sur un volume **inscriptible** par l'uid 1000. C'est le **dossier** qui doit l'être, pas seulement le fichier : à chaque écriture le serveur y crée un fichier temporaire `.tasks-*.tmp` (renommé ensuite) et le verrou `tasks.json.lock`. Le fichier `tasks.json` n'existe qu'après la première écriture réussie (droits 0600).
- **Fuseaux horaires** : le binaire a besoin de la base `zoneinfo`. Dans une image sans `/usr/share/zoneinfo`, au choix : copier ce dossier ; fournir `ZONEINFO=/chemin/zoneinfo.zip` (le fichier `$(go env GOROOT)/lib/time/zoneinfo.zip`, à copier depuis la machine de compilation) ; ou compiler avec `-tags timetzdata` (environ 400 Ko de plus ; la compilation est vérifiée, l'effet dans le conteneur ne l'est pas).
- **Arrêt** : le serveur s'arrête quand stdin se ferme, et aussi sur `SIGTERM` ou `SIGINT` (géré par la bibliothèque `mcp-go`) ; une opération en cours peut être interrompue, l'écriture atomique protège le fichier de données. L'agent doit garder son entrée ouverte, ce que fait tout client MCP.
- Les logs vont sur stderr ; stdout ne porte que le protocole MCP. Pas de réseau, pas de télémétrie.

**Échecs de démarrage** : le serveur écrit une ligne sur stderr et sort. ZeroClaw peut n'afficher que « le serveur ne démarre pas » : lancer d'abord le binaire à la main (point 6). Messages exacts (le texte après le dernier `:` vient du système et varie) :

| Situation | Code | Message sur stderr |
|---|---|---|
| ni `--config` ni `TASKS_MCP_CONFIG` | 2 | `tasks-mcp: no configuration file: pass --config <path> or set TASKS_MCP_CONFIG` |
| fichier de configuration absent ou illisible | 1 | `tasks-mcp: config: read: open … : <cause système>` |
| champ inconnu | 1 | `tasks-mcp: config: parse: json: unknown field "x"` |
| `data_file` relatif ou absent | 1 | `tasks-mcp: config: data_file is required and must be an absolute path` |
| fuseau inconnu ou base absente | 1 | `tasks-mcp: config: timezone: unknown time zone Europe/Paris` |
| dossier de données non inscriptible ou non créable | 1 | `tasks-mcp: data directory <dossier> is not writable: …` ou `tasks-mcp: data directory <dossier>: mkdir …` |

## 5. Déclaration dans ZeroClaw

Dans `config.toml` ([documentation de ZeroClaw](https://github.com/zeroclaw-labs/zeroclaw/blob/master/docs/setup-guides/mcp-setup.md), format à vérifier pour la version déployée) :

```toml
[mcp]
enabled = true

[[mcp.servers]]
name = "tasks"
transport = "stdio"
command = "/mcp/tasks-mcp"
args = ["--config", "/mcp/config.json"]
```

Hermes Agent et PicoClaw sont décrits dans `README.md`. Pour passer la configuration par l'environnement plutôt que par `--config`, utiliser la clé `env` de l'agent avec `TASKS_MCP_CONFIG` (syntaxe à vérifier dans la documentation de la version).

## 6. Tests à faire, dans cet ordre

**a. À la main, avec le vrai utilisateur** (`docker run --user 1000 …` avec le dossier `mcp` monté en `:ro` et le volume de données monté en lecture-écriture) : lancer la commande ci-dessous depuis le dossier de la configuration, avec un `data_file` de test. Elle a été exécutée telle quelle sous Git Bash (Windows) ; elle n'a pas été rejouée sous Linux. Le `sleep` garde stdin ouvert ; les réponses se repèrent par leur `id`.

```sh
{ printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_project","arguments":{"name":"Home"}}}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"add_task","arguments":{"title":"Pay rent","due":"2027-01-15","priority":"high"}}}'
  sleep 1
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}'
  sleep 2; } | ./tasks-mcp --config config.json
```

Résultat attendu sur stdout : `id` 1 → `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":…,"serverInfo":{"name":"tasks-mcp","version":"v0.1.0"}}}` ; `id` 2 → les huit outils ; `id` 3 → `…"text":"{\"project\":{\"id\":\"p_…\",\"name\":\"Home\",…}}"…` ; `id` 4 → `…"text":"{\"task\":{\"id\":\"t_…\",\"title\":\"Pay rent\",\"status\":\"todo\",\"priority\":\"high\",\"due\":\"2027-01-15\",…}}"…` ; `id` 5 → une liste d'une tâche. Aucune de ces réponses ne contient `isError`. Sur stderr : `tasks-mcp: starting v0.1.0, timezone Europe/Paris`.

**b. Dans ZeroClaw** : au démarrage de l'agent, stderr du serveur doit montrer la même ligne `starting`. Demander « liste mes projets » : réponse `{"projects":[]}` (ou la liste existante). Après le premier ajout, `ls -l /data/tasks-mcp` montre `tasks.json` en `-rw-------`, propriétaire 1000. Vérifier le nom réel des outils (point 2) avant d'écrire les règles d'approbation.

## 7. Exploitation

- **Sauvegarde** : `cp -p /data/tasks-mcp/tasks.json /data/tasks-mcp/tasks.json.bak`. Les lectures ne prennent pas le verrou et le remplacement du fichier est atomique : la copie est cohérente.
- **Remise à zéro** : arrêter l'agent, supprimer `tasks.json` (recréé à la première écriture).
- **Fichier de données illisible** (vide, corrompu, champ inconnu, trop gros, autre version de schéma) : le serveur ne le réécrit **jamais** et répond par une erreur claire à chaque appel (`data file is empty or corrupt: left untouched, fix or remove it`). Le restaurer depuis la sauvegarde ou le supprimer, agent arrêté.
- **Verrous** : `ls -la /data/tasks-mcp/`. `tasks.json.lock` n'existe que le temps d'une écriture (quelques millisecondes) ; un `tasks.json.lock.break` n'existe que le temps de reprendre un verrou périmé. Un verrou de plus de 30 secondes est périmé et repris automatiquement. Ne supprimer un `.lock` à la main que **agent arrêté**. Des fichiers `.tasks-*.tmp` ou `.write-check-*` qui traînent sont des restes d'un arrêt brutal : sans danger, supprimables.
- **Version** : `starting vX` sur stderr (pas d'option `--version`).

## 8. Ce qui a été vérifié, et ce qui ne l'a pas été

- **CI à chaque PR** : build, `go vet`, `golangci-lint` v2.14.0, `go test -race -shuffle` sous **Linux**, couverture des lignes modifiées (≥ 80 %), taille des PR (≤ 500 lignes Go). Les tests couvrent les huit outils par le protocole (`HandleMessage`), la concurrence entre goroutines **et entre processus** (3 processus écrivant le même fichier, aucune écriture perdue), les fichiers corrompus et le verrou périmé.
- **Binaire réel (Windows)** : poignée de main, huit outils listés, création, mise à jour, complétion, suppression, erreurs (date impossible, id inconnu), fichier corrompu laissé intact avec message clair, et cinq des échecs de démarrage du tableau du point 4 (messages relevés tels quels, sauf les causes système, qui sont celles de Windows) ; la variante `is not writable` n'a pas pu être provoquée, son texte vient du code.
- **Non exécuté** : le binaire Linux n'a été que compilé (`GOOS=linux`), pas lancé ; il n'a pas été essayé dans ZeroClaw ni dans le conteneur cible (uid 1000, volumes montés, dossier `mcp` en lecture seule). C'est la première vérification à faire (point 6).
- **Mesures** (Windows, 5000 tâches avec notes de 100 caractères) : fichier de 2,3 Mo, lecture complète 16 ms environ, écriture 30 ms environ.

## 9. Choix faits

- **Interface `Store`** (CRUD par entité, `UpdateTask(id, fn)`, `DeleteTask` qui renvoie la tâche supprimée) comme seule frontière avec le stockage ; `jsonStore` en est l'implémentation.
- **Verrou `.lock` portable** (`O_EXCL`, périmé après 30 s, garde `.lock.break`) plutôt que `flock`, pour compiler et tourner pareil sous Windows et Linux sans dépendance : voir `docs/adr/0001-portable-lock-file.md`.
- Un fichier illisible n'est **jamais** réécrit ; une écriture est atomique (temporaire, `fsync`, `rename`).
- Peu d'outils, descriptions écrites pour un petit modèle (formats, exemples). `list_tasks` ne renvoie pas les `notes` (gain de jetons) : `get_task` les donne. `add_task` n'a pas de `status` : une tâche naît `todo`.
- Les erreurs renvoyées par les outils ne répètent jamais la saisie et ne donnent jamais le chemin du fichier, et aucun titre ni note n'est écrit dans les logs.

## 10. Limites connues

- Un seul utilisateur, sans authentification ; pas de sous-tâches, récurrence, pièces jointes ni heure d'échéance.
- **Pas de recherche par texte** dans `list_tasks`, et pas de page suivante au-delà de `max_results` (`truncated` invite à resserrer les filtres) : un assistant qui cherche « la tâche du dentiste » doit filtrer par projet, tag ou échéance, ou lister puis lire.
- Projets figés : ni renommage, ni archivage, ni suppression.
- Verrou fondé sur l'âge d'un fichier : un saut d'horloge, ou un processus bloqué plus de 30 s au milieu d'une écriture, peut faire se chevaucher deux écritures ; après un arrêt brutal, les écritures attendent jusqu'à 30 s puis reprennent.
- Le fichier entier est lu et réécrit à chaque opération. Un fichier de plus de 16 Mio n'est plus lu (la limite s'applique à la lecture) : un fichier saturé de texte non ASCII pourrait l'atteindre, auquel cas baisser `max_tasks`.
- Sous Windows, un lecteur tenant le fichier ouvert peut faire échouer le `rename` final ; la cible est Linux.
- Le binaire n'embarque pas les fuseaux horaires par défaut (point 4).

## 11. Pour passer à une base de données, une API et une interface (non codé)

- Remplacer `jsonStore` par une implémentation SQL de `Store` : le verrou, le fichier temporaire, `max_tasks` à la main et le tirage d'id avec nouvelles tentatives disparaissent ; `UpdateTask(id, fn)` devient une transaction. Les outils ne changent pas.
- Déplacer `model.go` (validation, `TaskUpdate.apply`, `setStatus`) et `store.go` dans un paquet `internal/` pour les partager avec une API HTTP.
- Pour une interface : filtrage et pagination côté base (`ListTasks` renvoie tout aujourd'hui), recherche par texte, gestion des projets, et un identifiant d'utilisateur sur chaque entité pour du multi-utilisateur.
- Import : `tasks.json` porte `schema_version` ; la lecture stricte de `jsonStore.load` sert de base à un import.
