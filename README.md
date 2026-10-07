# tasks-mcp

Serveur MCP (transport stdio) qui donne à un assistant IA un gestionnaire de tâches et de projets simple, pour un seul utilisateur. Les données sont stockées dans un fichier JSON. Le serveur n'utilise pas le réseau.

> État : squelette. Le serveur démarre et vérifie sa configuration, mais n'expose pas encore d'outil (voir les issues du dépôt).

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
