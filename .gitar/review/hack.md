# Review instructions

## Privacy
- Never log or persist caption text or audio, and never put them into spans, metrics or logs. Only lengths and counts.
- No secrets in the repo, manifests or CI. `SPEAKER_TOKEN`, the Dash0 token and API keys come from env or k8s secrets.

- Caption text may leave the server only to Elasticsearch, only for final captions, and only when the speaker's opt-in (`save: true`) is set on that caption.

## Reliability
- Every outbound call (OpenAI, anything else) has a context timeout, its own span and a recorded error.
- SSE handlers return when the request context is cancelled. No goroutine may outlive its listener.
- Broadcast never blocks on a slow listener. Per-listener channels are bounded, and when one is full the oldest interim update is dropped.
- On SIGTERM the server stops accepting, sends `event: bye` to listeners and exits within the grace period.

## Security at the trust boundary
- `POST /api/rooms/{room}/captions` requires `Authorization: Bearer $SPEAKER_TOKEN`.
- Room names match `^[a-z0-9-]{1,32}$`. Caption text is capped at 500 chars. Anything else is a 400 or 401.

## Accessibility
- The caption region has `aria-live="polite"`.
- Text contrast is at least 4.5:1, in both normal and high-contrast mode.
- Font size is adjustable, and nothing is conveyed by color alone.

## Kubernetes
- Manifests keep requests and limits, readiness and liveness probes, and `terminationGracePeriodSeconds`.
- One replica: the fan-out is in memory. A change to more replicas must add shared pub/sub in the same PR.

## Workflow
- One feature per PR, small diffs.
- In GitHub Actions, user-controlled values go through `env:`, never `${{ }}` inside `run:`.
