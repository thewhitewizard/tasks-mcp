## Agent skills

### Issue tracker

Issues live in GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Règles de PR

- **≤ 500 lignes Go par PR**, tests inclus : lignes ajoutées + supprimées dans tous les `*.go`, comparées au merge-base avec `main`. Vérification : `bash scripts/check-pr-size.sh`. La CI et un hook Claude Code (avant `git commit`, `git push`, `gh pr create`) appliquent la limite.
- **≥ 80 % des lignes Go de production modifiées couvertes par les tests** : lignes ajoutées ou modifiées hors `_test.go`, comparées au merge-base avec `main` ; seules les lignes exécutables comptent. Vérification : `bash scripts/check-coverage.sh` (`COVERAGE_MIN` change le seuil). La CI l'applique à chaque PR.
- **Si l'implémentation approche la limite, s'arrêter** : livrer ce qui est cohérent et proposer un ticket de suite plutôt que dépasser.
- **Un ticket = une PR.**
- **Les tests ne dépendent pas de leur ordre** : lancer `go test -shuffle=on ./...` ; rejouer un échec avec `go test -shuffle=<graine> ./...`. `-race` ne fonctionne pas en local sous Windows : la CI le lance.
- **Lancer `golangci-lint run ./...` avant de pousser** : il doit passer sans aucun problème (v2.14.0 en local).
- **`go build`, `go vet` et `go test ./...` passent** avant toute PR.
- **README à jour dans la même PR** : toute PR qui ajoute ou modifie un outil MCP, un flag, une variable d'environnement ou un champ de configuration met à jour `README.md`.
- **Pas de code spéculatif** pour des tickets futurs.
- **Stdout est réservé au protocole MCP** : logs sur stderr via `log`, jamais `fmt.Print*`.
- **Aucun accès réseau, aucune télémétrie** ; aucune donnée personnelle (titres de tâches, notes) dans les logs ni les messages d'erreur ; les tests n'utilisent que `t.TempDir()`.
- **Seul `mcp-go` v1.1.1 est autorisé** hors bibliothèque standard : toute autre dépendance se demande d'abord.
- Les outils d'édition convertissent `\uXXXX` en caractères réels : écrire `\U0000XXXX` ou `string(rune(n))`.

## Skills Go

Charger les skills `cc-skills-golang:<nom>` selon la zone touchée, pas tous.

| Zone | Skills |
|---|---|
| Toujours (tout ticket Go) | `golang-testing`, `golang-error-handling`, `golang-safety` |
| Nettoyage du texte des tâches, fichiers, permissions | `golang-security` |
| Verrou, accès concurrents | `golang-concurrency`, `golang-context` |
| Point d'entrée (`--config`, variable d'environnement, codes de sortie, stderr) | `golang-cli` |
| Structures de configuration et de tâches, interface `Store`, horloge | `golang-structs-interfaces` |
| Ajout ou mise à jour d'une dépendance | `golang-dependency-management`, `golang-popular-libraries`, `golang-pkg-go-dev` |
| Relecture / qualité | `golang-lint`, `golang-naming`, `golang-code-style`, `golang-documentation`, `golang-modernize` |

**À ne pas utiliser** : `golang-grpc`, `golang-graphql`, `golang-database`, `golang-swagger` ; frameworks d'injection ; bibliothèques `samber/*` ; `golang-spf13-cobra`, `golang-spf13-viper`, `golang-stretchr-testify` (stdlib seule hors `mcp-go`) ; `golang-observability` (les logs se limitent à `log` sur stderr).
