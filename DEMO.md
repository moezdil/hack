# Demo log

Running notes for the 5:30 story. Real numbers only.

## Timeline (Mountain Time, Nov 9)

- 11:12 PR #1 (bootstrap) opened. Gitar: CI's `go vet` and `go test` exit 1 on a module with no packages yet. Confirmed locally, fixed with `if: hashFiles('**/*.go') != ''` on the Go steps.
- 11:20 PR #3 (server, pages, tests) merged.
- 11:25 PR #4 (Argo CD deploy) merged. Build-and-deploy workflow pushed `ghcr.io/moezdil/hack:f8eacd4`; Argo CD went OutOfSync -> Synced, old starter pods failed their `app-code` configmap mount while being pruned (expected, noted in the starter README), new pod Running at 11:31.
- 11:32 End-to-end test on https://hack.cnh.radarhq.io: POST caption -> SSE listener received it; request without the speaker token -> 401.

## Gitar findings

- #1: CI fails on an empty Go module. Real, fixed.

## Radar

- Cluster `hack`, namespace `team-hack`, Application `app` (project `team-hack`), image tag = commit SHA.

## Dash0

- Pending: OTLP export PR.
