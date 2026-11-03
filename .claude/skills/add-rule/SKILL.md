---
name: add-rule
description: Add or change a filter rule in dealsieve end to end (config, rule engine order, reason text, tests, page, docs). Use when asked to block or allow a new kind of deal, add a rule setting, change how an existing rule decides, or change the watchlist/recurrence/temperature behaviour.
---

# Add or change a rule

The rule engine (`internal/rules`) is pure: `Evaluate(Deal, Facts) Result`. Everything it needs comes from config (`config.Rules`), the deal itself, or `Facts` gathered by the pipeline (user actions, recurrence counts). Keep it that way — no I/O in `rules`.

## Steps
1. **Decide where it sits in the order** in `blockReason` (see the list in `CLAUDE.md`). The first block reason wins. Decide explicitly whether watchlist hits are exempt (recurrence and temperature are; mutes, hides and keywords are not) and write the why in a short comment.
2. **Config, if the rule is configurable:** add the field to `config.Rules` (YAML + JSON tags; JSON is what `/api/rules` shows on the page), a default in `defaults()`, a check in `validate()`, and a test in `config_test.go`. Add it to `config.example.yaml`.
3. **Facts, if it needs stored data:** extend `rules.Facts`, fill it in `pipeline.evaluate` (or `actionFacts` for user actions). If it needs new columns, add a migration `internal/store/migrations/NNN_*.sql`.
4. **Reason text:** short, lowercase, human-readable, with the concrete value (`"block keyword: gratisprobe"`, `"too cold: 42° < 100°"`). It's shown verbatim on the page and in notifications.
5. **Tests:** add rows to the table in `rules_test.go` (`TestEvaluate`), including the interaction with a watchlist hit. If the pipeline changed, add a `pipeline_test.go` case.
6. **Page:** if the rule has settings, check the Rules tab (`web/static/js/render.js`) shows them.
7. **Docs:** README "What it filters" and config notes; `CLAUDE.md`'s rule order if it changed.

## Verify
```bash
gofmt -l . && go vet ./... && go test -race ./... && go test -tags e2e ./test/e2e/
```
Remember that existing verdicts are recomputed only for the last 7 days (at startup and after actions); say in the PR what happens to older deals.
