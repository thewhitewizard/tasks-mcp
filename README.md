# tasks-mcp

Serveur MCP (transport stdio) qui donne à un assistant IA un gestionnaire de tâches et de projets simple, pour un seul utilisateur. Les données sont stockées dans un fichier JSON. Le serveur n'utilise pas le réseau.

> État : en construction. Le serveur expose pour l'instant les outils de lecture `list_projects`, `list_tasks` et `get_task`, et d'écriture `create_project`, `add_task`, `update_task`, `complete_task` et `delete_task` (voir les issues du dépôt pour la suite).

## Compilation

```sh
go build -o tasks-mcp .
# Linux (déploiement) :
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tasks-mcp .
```

## Lancement

```sh
tasks-mcp --config /chemin/vers/config.json
# ou
TASKS_MCP_CONFIG=/chemin/vers/config.json tasks-mcp
```

Le flag `--config` l'emporte sur la variable `TASKS_MCP_CONFIG`. Codes de sortie : 0 (arrêt normal ou `-h`), 1 (configuration invalide, dossier de données inutilisable, erreur du serveur), 2 (erreur d'usage). Les messages vont sur stderr ; stdout est réservé au protocole MCP.

## Configuration

Fichier JSON strict (un champ inconnu est refusé). Voir `config.example.json`.

| Champ | Obligatoire | Défaut | Rôle |
|---|---|---|---|
| `data_file` | oui | | Chemin **absolu** du fichier de données (sous Windows, `C:\...` ; l'exemple `/var/lib/...` vise Linux). |
| `timezone` | oui | | Fuseau IANA (ex. `Europe/Paris`) ; `Local` est refusé. |
| `max_tasks` | non | 5000 | Nombre maximal de tâches. |
| `max_results` | non | 50 | Nombre maximal de tâches renvoyées par une liste. |
| `lock_timeout_seconds` | non | 5 | Attente maximale du verrou d'écriture ; doit être inférieur à 30. |

Au démarrage, le dossier du fichier de données est créé s'il manque (droits 0700), puis le serveur vérifie qu'il peut y écrire. Sinon il s'arrête avec un message sur stderr. Le fichier de données lui-même n'est pas créé au démarrage.

## Écritures concurrentes

Plusieurs processus `tasks-mcp` peuvent écrire le même fichier de données : chaque écriture prend un verrou (`<data_file>.lock`, avec une garde `<data_file>.lock.break`), relit le fichier, le modifie puis le remplace de façon atomique. Les lectures ne prennent pas le verrou. Un verrou abandonné par un processus mort est repris après 30 secondes ; en attendant, les écritures échouent au bout de `lock_timeout_seconds` (réglage appliqué au câblage du Store aux outils). Ces fichiers `.lock` ne doivent pas être supprimés à la main pendant que le serveur tourne. Voir `docs/adr/0001-portable-lock-file.md`.

## Outils

Les réponses sont du JSON compact. Les erreurs métier sont renvoyées comme résultats d'outil en erreur, sans répéter les valeurs fournies. Les outils de lecture sont déclarés `readOnlyHint: true`, `destructiveHint: false`, `openWorldHint: false` ; les outils d'écriture `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: false`, sauf `delete_task`, le seul avec `destructiveHint: true`. Les horodatages sont écrits dans le fuseau de la configuration. Un argument facultatif à `null` est traité comme absent ; un argument d'un autre type que celui attendu est une erreur. Les messages d'erreur ne donnent jamais le chemin du fichier de données. Les descriptions destinées à l'assistant sont en anglais.

### `list_projects`

Aucun paramètre. Réponse : `{"projects":[{"id":"p_k3x9aq","name":"Home","description":"…","created_at":"2026-07-14T09:30:00+02:00"}]}`, triés par nom (sans tenir compte de la casse), puis par id. `description` n'apparaît que si elle existe. Aucun projet : `{"projects":[]}`.

### `list_tasks`

| Paramètre | Rôle |
|---|---|
| `project` | Id d'un projet (`p_` + 6 caractères) ; une erreur si le projet n'existe pas. |
| `status` | `todo`, `doing` ou `done`. `done` fonctionne sans `include_done`. |
| `due_before` | Jour `AAAA-MM-JJ`, inclus ; les tâches sans échéance sont exclues. |
| `tag` | Tag, sans tenir compte de la casse. |
| `include_done` | Booléen, `false` par défaut : sans lui (et sans `status`), les tâches terminées sont omises. |

Les filtres se combinent (ET). Réponse : `{"tasks":[…],"truncated":true}` ; `truncated` n'apparaît que si plus de `max_results` tâches correspondent, et `tasks` vaut `[]` quand rien ne correspond. Les tâches n'ont pas leur champ `notes` : `get_task` les donne. Ordre : tâches ouvertes avant les terminées, puis échéance croissante (sans échéance en dernier), priorité (`high`, `normal`, `low`), date de création, id. Une tâche comporte `id` (`t_` + 6 caractères), `title`, `status`, `priority`, `due`, `project`, `tags`, `created_at`, `updated_at` et `completed_at` (les champs vides sont omis, `tags` vaut `[]`).

### `get_task`

Paramètre obligatoire `id` (`t_` + 6 caractères ; majuscules et espaces autour acceptés). Réponse : `{"task":{…}}` avec tous les champs, `notes` comprises (omises quand elles sont vides). Erreurs : `id is required` si absent, `id must be text` si ce n'est pas du texte, et une erreur qui renvoie vers `list_tasks` si aucune tâche n'a cet id.

### `create_project`

Paramètres : `name` (obligatoire, 100 caractères au plus) et `description` (500 au plus). Réponse : `{"project":{…}}`, le projet créé. Les noms sont uniques sans tenir compte de la casse : un doublon donne une erreur qui contient l'id du projet existant. 200 projets au plus.

### `add_task`

| Paramètre | Rôle |
|---|---|
| `title` | Obligatoire, 200 caractères au plus, une seule ligne. |
| `notes` | 2000 caractères au plus ; les sauts de ligne sont conservés. |
| `project` | Id d'un projet existant (`p_` + 6 caractères, majuscules acceptées). |
| `due` | Jour `AAAA-MM-JJ` (pas d'heure). |
| `priority` | `low`, `normal` (défaut) ou `high`. |
| `tags` | Liste de textes : 10 au plus, 30 caractères chacun ; mis en minuscules, sans doublon. |

La tâche est créée `todo`. Réponse : `{"task":{…}}`, la tâche créée avec son id. Les caractères de contrôle et invisibles sont retirés des textes. Au-delà de `max_tasks`, l'ajout est refusé.

### `update_task`

Paramètres : `id` (obligatoire) et, facultatifs, `title`, `notes`, `status`, `priority`, `due`, `project`, `tags` (mêmes formats et limites qu'`add_task`). Réponse : `{"task":{…}}`, la tâche entière.

- Un champ absent (ou `null`) reste inchangé ; les espaces autour d'un texte sont ignorés (un texte fait d'espaces efface donc `notes`, `due` ou `project`).
- Une chaîne vide efface `notes`, `due` et `project` ; `tags` remplace la liste (`[]` l'efface).
- `title`, `status` et `priority` ne s'effacent pas : une valeur vide est une erreur.
- Passer `status` à `done` renseigne `completed_at` ; tout autre statut l'efface.
- `updated_at` ne change que si un champ change vraiment ; fournir les valeurs actuelles n'est pas une erreur.
- Aucun champ fourni : erreur `nothing to update`.

### `complete_task`

Paramètre : `id`. Passe la tâche à `done` et renseigne `completed_at`. Répétable sans effet : une tâche déjà terminée est renvoyée telle quelle, `completed_at` et `updated_at` inchangés.

### `delete_task`

Paramètre : `id` (`t_` + 6 caractères, majuscules et espaces autour acceptés). Supprime la tâche définitivement et renvoie `{"deleted":{…}}`, la tâche supprimée, pour que l'assistant confirme ce qui a disparu. Un id inconnu ou déjà supprimé donne une erreur qui renvoie vers `list_tasks`. C'est le seul outil destructeur : la lecture de la tâche et sa suppression se font en une seule opération sous verrou.
