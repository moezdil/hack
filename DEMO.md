# Demo log, Nov 9 2026 (Mountain Time)

Real numbers only. Links go to the tools.

## Timeline

- 11:12 PR #1 bootstrap. Gitar's first review: CI's `go vet` / `go test` exit 1 on a module with no Go packages. Reproduced locally, fixed before merge.
- 11:20 PR #3 server, pages, tests merged.
- 11:25 PR #4 Argo CD deploy merged. Image `ghcr.io/moezdil/hack:f8eacd4`, Application `app` in `team-hack` OutOfSync -> Synced, old starter pods pruned, new pod Running at 11:31.
- 11:32 End-to-end test on https://hack.cnh.radarhq.io: POST caption -> SSE listener; no token -> 401.
- 11:36 PR #6 OpenTelemetry export (traces, metrics, logs) to Dash0. First spans visible within a minute.
- 11:53 Speaker test with a real microphone and a phone over mobile data: captions arrived, "slow but works".
- 12:02 to 12:26 UI (PRs #7, #8, #14), landing page, demo feeder CronJob (#12), README and architecture diagram (#13).
- 12:50 to 14:00 no merges (talk window).
- 15:38 PR #15 Elasticsearch search and PR #16 translation merged.
- 15:40 Radar issues on `team-hack`: `demo-feeder` CronJob critical. Investigation started.
- 15:43 Fix PR #17 merged. Agent0 report on the day's telemetry received.
- 15:45 PR #18 from Agent0's finding (no Kubernetes attributes on telemetry) merged.
- 15:48 Translation verified live: a listener on `?lang=tr` received "Her konuşma için canlı altyazılar, uygulama gerekmez." right after the original line.
- 15:52 Room `demo` receiving captions again (Dash0). Listener page fixed for 390 px phones (PR #21).

## Radar: the real issue

Radar `issues` for `team-hack` showed two critical rows: `CronJob demo-feeder` with `CreateContainerConfigError` (311 warning events) and `Job demo-feeder-29904399` `DeadlineExceeded` (61 failed jobs). The kubelet message: `container has runAsNonRoot and image has non-numeric user (curl_user), cannot verify user is non-root`.

Radar's AI investigation (confidence 0.82) on the CronJob: root cause is `runAsNonRoot: true` without a numeric `runAsUser`; the `curlimages/curl` image's user is `curl_user`, which the kubelet cannot verify. Not a regression, present since the CronJob was created. The CoreDNS rewrite warning in the cluster was correctly ruled out as unrelated. Suggested fix: set a numeric `runAsUser`.

Fix: PR #17 sets `runAsUser: 100`. Verified at 15:52: Radar events show `Created container: feeder` after the rollout, the old `CreateContainerConfigError` stopped at 22:45 UTC, and Dash0's `caption.delay{room="demo"}` started counting. Found in Radar, fixed through a Gitar-reviewed PR, confirmed in Dash0.

Investigation link: https://app.radarhq.io/c/FzTAhw5Ukok/investigations/chat_vaXvy4SR40cPpTYd?org=org_NCJbssDGEBsU

## Dash0: Agent0 on the day's telemetry

Window 18:00 to 22:41 UTC. Findings:

- No errors: 104 spans, all UNSET status; 61 logs, no WARN or ERROR.
- No dropped broadcasts: 52 `caption.broadcast` spans, all `dropped=0`.
- `caption.delay` p50 about 1.1 s, p95 about 2.8 s (received and sent stages, histogram estimate). One outlier in the 5 to 10 s bucket.
- Room `demo` had zero captions although the CronJob should send one per minute. Agent0 could not see why (no cluster access) and pointed at the CronJob. Radar gave the cause, see above. Two tools, one incident, each with its half of the evidence.
- `sse.reconnects` reached 34 for room `talk` in ten minutes, about one every 20 s, after the speaker stopped. Likely an idle timeout on the path between phone and service. Open item; the 15 s server ping did not prevent it.
- Resources carried no Kubernetes attributes, so restarts could not be told apart from redeploys. Fixed in PR #18 (`OTEL_RESOURCE_ATTRIBUTES` from the downward API).

Thread: https://app.dash0.com/goto/agent0?thread_id=e7acc9d2-2d3c-443c-b591-63d311725eae&dataset=default&org=6ebde1cf-921d-4f8f-a703-467c146f1c63

## Gitar

- #1: CI fails on an empty Go module. Real, fixed before merge.
- #17: approved with a medium-risk note on changing the container uid, which is exactly the change intended.
- Review instructions in `.gitar/review/hack.md`; Gitar runs on every PR.

## Elasticsearch

Serverless project, `captions` index with `semantic_text`. Verified end to end: the question "why do containers get restarted" returned "the kubelet restarts pods when the node is under memory pressure". Off unless the speaker opts in.

## Open

- Reconnect pattern on `talk` (above).
- SLO (95% of captions within 2 s over 1 h): p95 today was about 2.8 s, so the target is not met yet; the biggest share is the browser's speech recognition latency, which the `received` stage includes.
