---
name: add-rule
description: Add or change a filter rule in dealsieve end to end (settings, rule engine order, reason text, tests, Settings page, docs). Use when asked to block or allow a new kind of deal, add a rule setting, change how an existing rule decides, or change the watchlist/recurrence/temperature behaviour.
---

# Add or change a rule

The rule engine (`internal/rules`) is pure: `Evaluate(Deal, Facts) Result`. Everything it needs comes from the settings (`settings.Rules`, converted for the engine), the deal itself, or `Facts` gathered by the pipeline (user actions, recurrence counts). Keep it that way — no I/O in `rules`.

## Steps
1. **Decide where it sits in the order** in `blockReason` (see the list in `CLAUDE.md`). The first block reason wins. Decide explicitly whether watchlist hits are exempt (recurrence and temperature are; mutes, hides and keywords are not) and write the why in a short comment.
2. **Settings, if the rule is configurable:** settings live in `internal/settings` (the JSON document served by `/api/settings`). A new setting needs a default in `Defaults()`, a validation rule in `Validate()` (keyed by field path, with a test in `settings_test.go`), a change line in `Summarize()` (for the history), and its conversion for the rules engine. Add the control to the Settings page (`web/settings.html`, `web/static/js/settings*.js`, with a test for any new pure helper) and an entry to the docs-site configuration page. Never add a YAML file or a new env var for something a user edits.
3. **Facts, if it needs stored data:** extend `rules.Facts`, fill it in `pipeline.evaluate` (or `actionFacts` for user actions). If it needs new columns, add a migration `internal/store/migrations/NNN_*.sql`.
4. **Reason text:** short, lowercase, human-readable, with the concrete value (`"block keyword: gratisprobe"`, `"too cold: 42° < 100°"`). It's shown verbatim on the page and in notifications.
5. **Tests:** add rows to the table in `rules_test.go` (`TestEvaluate`), including the interaction with a watchlist hit. If the pipeline changed, add a `pipeline_test.go` case.
6. **Page:** check the Settings page shows and saves the new setting, and that reasons render in the inbox (`web/static/js/render.js`).
7. **Docs:** README "What it filters", the docs-site configuration page and how-it-works rule table; `CLAUDE.md`'s rule order if it changed.

## Verify
```bash
gofmt -l . && go vet ./... && go test -race ./... && go test -tags e2e ./test/e2e/
```
Remember that existing verdicts are recomputed only for the last 7 days (at startup, after actions and after a settings save); say in the PR what happens to older deals.
