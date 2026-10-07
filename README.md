# tasks-mcp

Serveur MCP (transport stdio) qui donne à un assistant IA un gestionnaire de tâches et de projets simple, pour **un seul utilisateur**. Les données tiennent dans un fichier JSON. Le serveur n'utilise pas le réseau.

Huit outils : trois de lecture, quatre d'écriture, un destructeur. Le glossaire est dans `CONTEXT.md`, les règles de contribution dans `CLAUDE.md`, et la décision sur le verrou d'écriture dans `docs/adr/0001-portable-lock-file.md`.

## Outils

| Outil | Rôle | Type |
|---|---|---|
| `list_projects` | Liste les projets | lecture |
| `list_tasks` | Liste et filtre les tâches (sans leurs notes) | lecture |
| `get_task` | Une tâche en entier, notes comprises | lecture |
| `create_project` | Crée un projet | écriture |
| `add_task` | Crée une tâche | écriture |
| `update_task` | Modifie une tâche (mise à jour partielle) | écriture |
| `complete_task` | Passe une tâche à `done` | écriture |
| `delete_task` | Supprime une tâche pour de bon | **destructeur** |

Les réponses sont du JSON compact. Les erreurs métier sont renvoyées comme résultats d'outil en erreur, sans répéter les valeurs fournies ni donner le chemin du fichier de données. Les outils de lecture sont déclarés `readOnlyHint: true`, `destructiveHint: false`, `openWorldHint: false` ; les outils d'écriture `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: false`, sauf `delete_task`, le seul avec `destructiveHint: true`. Les horodatages sont en ISO 8601 (RFC 3339), à la seconde, dans le fuseau de la configuration. Un argument facultatif à `null` est traité comme absent ; un argument d'un autre type que celui attendu est une erreur. Les descriptions destinées à l'assistant sont en anglais.

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

## Compilation

Binaire statique, sans CGO :

```sh
# Pour le poste de développement :
go build -ldflags "-X main.version=v0.1.0" -o tasks-mcp .

# Pour un serveur Linux x86-64 :
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=v0.1.0" -o tasks-mcp .
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
| `max_results` | non | 50 | Nombre maximal de tâches renvoyées par `list_tasks`. |
| `lock_timeout_seconds` | non | 5 | Attente maximale du verrou d'écriture ; doit être inférieur à 30. |

Au démarrage, le dossier du fichier de données est créé s'il manque (droits 0700), puis le serveur vérifie qu'il peut y écrire en y créant puis supprimant un fichier temporaire. Sinon il s'arrête avec un message sur stderr. Le fichier de données lui-même n'est créé qu'au premier ajout (droits 0600).

**Le dossier du fichier de données doit être inscriptible, pas seulement le fichier** : le serveur y crée, le temps d'une écriture, un fichier temporaire, le verrou `<data_file>.lock` et sa garde `<data_file>.lock.break`.

### Conteneur minimal

Le binaire n'embarque **pas** la base des fuseaux horaires. Dans un conteneur sans `/usr/share/zoneinfo` (image `scratch`), `timezone` ne peut pas être chargé et le serveur refuse de démarrer. Deux solutions : copier le dossier `zoneinfo` dans l'image, ou fournir l'archive de Go et la désigner par `ZONEINFO=/chemin/zoneinfo.zip` (le fichier `$(go env GOROOT)/lib/time/zoneinfo.zip`).

## Sécurité et robustesse

- Aucun outil n'accepte un chemin de fichier : le fichier de données vient **uniquement** de la configuration. Aucun accès réseau, aucune télémétrie.
- Le texte des tâches vient de l'utilisateur ou de l'assistant, qui peut recopier du texte venu d'ailleurs : les caractères de contrôle et les caractères invisibles sont supprimés. Limites : titre 200 caractères, notes 2000, 10 tags de 30 caractères, nom de projet 100, description 500, 200 projets, `max_tasks` tâches, fichier de 16 Mio au plus.
- Aucun titre ni note dans les logs ni dans les messages d'erreur ; les erreurs système ne donnent pas le chemin du fichier.
- Le fichier de données n'est **jamais écrasé** s'il est vide, corrompu, trop gros, d'une autre version de schéma (`schema_version` ≠ 1) ou avec un champ inconnu : l'outil répond par une erreur claire et le fichier reste intact.
- Écriture atomique : fichier temporaire dans le même dossier, `fsync`, puis `rename`.

## Écritures concurrentes

Plusieurs processus `tasks-mcp` peuvent écrire le même fichier de données : chaque écriture prend un verrou (`<data_file>.lock`, avec une garde `<data_file>.lock.break`), relit le fichier, le modifie puis le remplace de façon atomique. Les lectures ne prennent pas le verrou. Un verrou abandonné par un processus mort est repris après 30 secondes ; en attendant, les écritures échouent au bout de `lock_timeout_seconds`. Ces fichiers `.lock` ne doivent pas être supprimés à la main pendant que le serveur tourne. Voir `docs/adr/0001-portable-lock-file.md`.

## Déclarer le serveur dans un agent

Dans les exemples, remplacez `/opt/tasks-mcp/tasks-mcp` et `/opt/tasks-mcp/config.json` par vos chemins. Le fichier de données (`data_file`) doit être sur un volume inscriptible par l'utilisateur de l'agent, hors de tout dossier monté en lecture seule.

### Hermes Agent

`~/.hermes/config.yaml`, clé `mcp_servers` ([documentation](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp)) :

```yaml
mcp_servers:
  tasks:
    command: "/opt/tasks-mcp/tasks-mcp"
    args: ["--config", "/opt/tasks-mcp/config.json"]
```

### ZeroClaw

`config.toml`, section `[mcp]` ([documentation](https://github.com/zeroclaw-labs/zeroclaw/blob/master/docs/setup-guides/mcp-setup.md)) :

```toml
[mcp]
enabled = true

[[mcp.servers]]
name = "tasks"
transport = "stdio"
command = "/opt/tasks-mcp/tasks-mcp"
args = ["--config", "/opt/tasks-mcp/config.json"]
```

### PicoClaw

`config.json`, clé `tools.mcp.servers` ([dépôt](https://github.com/sipeed/picoclaw)) :

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "tasks": {
          "enabled": true,
          "command": "/opt/tasks-mcp/tasks-mcp",
          "args": ["--config", "/opt/tasks-mcp/config.json"]
        }
      }
    }
  }
}
```

Les formats de ces trois agents évoluent : en cas de doute, vérifiez la documentation de votre version. Pour passer le chemin de la configuration par l'environnement plutôt que par `--config`, utilisez la clé `env` de l'agent avec `TASKS_MCP_CONFIG`.

## Test manuel en stdio

Cette commande lance le serveur, fait la poignée de main MCP, liste les outils, crée un projet et une tâche, puis liste les tâches. Le `sleep` garde stdin ouvert : le serveur s'arrête dès qu'il se ferme, et annule alors le travail en cours.

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

Les réponses (sur stdout, une par ligne) se repèrent par leur `id`. Le message de démarrage est sur stderr. Les `sleep` entre les appels laissent chaque écriture se terminer avant la suivante : un vrai client attend la réponse. Utilisez un `config.json` dont `data_file` pointe vers un dossier de test, pas vers vos vraies données.

## Limites connues

- **Un seul utilisateur**, sans authentification ni droits. Pas de sous-tâches, de récurrence, de pièces jointes ni d'heure d'échéance (`due` est un jour).
- **Pas de recherche par texte** : `list_tasks` filtre par projet, statut, échéance et tag, pas par mot du titre ou des notes. Au-delà de `max_results` tâches correspondantes, il n'y a pas de page suivante : `truncated` invite à resserrer les filtres.
- **Projets figés** : on peut en créer, pas les renommer, les archiver ni les supprimer.
- Chaque opération lit ou réécrit **tout le fichier** (5000 tâches au plus : environ 2,3 Mo avec des notes de 100 caractères, une lecture complète en une vingtaine de millisecondes, une écriture en une trentaine, mesurées sous Windows) : suffisant pour un usage personnel, pas pour du volume.
- Le verrou d'écriture repose sur l'âge d'un fichier (30 secondes) : un saut d'horloge, ou un processus bloqué plus de 30 secondes au milieu d'une écriture, peut faire se chevaucher deux écritures. Après un arrêt brutal, les écritures attendent jusqu'à 30 secondes. Voir l'ADR.
- Sous Windows, un lecteur qui tient le fichier de données ouvert peut faire échouer le `rename` final d'une écriture. La cible de déploiement est Linux (la CI y exécute les tests, `-race` compris).
- Le binaire n'embarque pas la base des fuseaux horaires (voir « Conteneur minimal »).

## Pour aller plus loin : base de données, API, interface

Rien de cela n'est codé. Les points d'appui :

- **`Store`** (`store.go`) est l'unique frontière avec le stockage : CRUD par entité, `UpdateTask(id, fn)` (qui se traduit en transaction) et `DeleteTask`. Une base de données remplacerait `jsonStore` sans toucher aux outils ; le verrou, le fichier temporaire, `max_tasks` à la main et le tirage d'id avec nouvelles tentatives disparaîtraient avec lui.
- **Validation et règles métier** (`model.go` : `newTask`, `newProject`, `TaskUpdate.apply`, `setStatus`) sont indépendantes du transport MCP. Pour les partager avec une API HTTP, les déplacer dans un paquet `internal/` avec le `Store`.
- **Ce qu'il faudrait ajouter côté Store** pour une interface : filtrage et pagination côté base (`ListTasks` renvoie aujourd'hui tout), recherche par texte, gestion des projets (renommer, archiver, supprimer), et un identifiant d'utilisateur sur chaque entité si l'on veut du multi-utilisateur.
- **Migration** : le fichier porte `schema_version` ; un import `tasks.json` vers la base se fait avec la même lecture stricte que `jsonStore.load`.
