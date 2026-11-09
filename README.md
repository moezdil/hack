# Readalong

**Every talk, every ear.** Live captions for conference talks, on the listener's own phone. Scan a QR code, read along. No app, no account, nothing stored.

![Listener page filling with captions](docs/demo.gif)

**Live:** https://hack.cnh.radarhq.io · **Try it:** open [/r/demo](https://hack.cnh.radarhq.io/r/demo) on your phone, a demo room gets a new line every minute.

## Why

Most sessions have no captions. Deaf and hard-of-hearing attendees, non-native speakers and the back row lose the talk. Human captioning is rare and expensive. Readalong gives any talk live captions in under a minute, for about nothing per talk.

## What listeners get

- Captions on their own phone, from a QR code on the slide. No install, no sign-up.
- **A+ / A−** text size, **high contrast** mode, screen-reader friendly (`aria-live`).
- **Translation**: the speaker talks in any of 12 languages; listeners pick theirs, finished lines arrive translated.
- **Search what was said**, by meaning, when the speaker allows it.
- A badge that says how far behind the captions are. Honest about delay.
- **Share** the link with the person next to you.

## Use it in three steps

1. Speaker opens `/s/<room>` in Chrome, pastes the speaker token, picks the language they speak, presses **Start captions**.
2. Listeners scan the QR on the slide or open `/r/<room>`.
3. Talk. That's it.

QR for a room: `qrencode -o qr.png https://hack.cnh.radarhq.io/r/<room>`

<img src="docs/qr-talk.png" alt="QR code for room talk" width="150">

## How it works

![Architecture](docs/architecture.svg)

Editable: [docs/architecture.excalidraw](docs/architecture.excalidraw)

- **Speaker page**: Chrome's speech recognition, restarted automatically when Chrome stops it.
- **Server**: one Go binary, standard library HTTP. Validates room, text length and token; fans out over server-sent events; keeps the last 50 lines for late joiners; never blocks on a slow phone; tells listeners to reconnect on shutdown.
- **Translation**: OpenAI API, once per language wanted in the room; token usage and cost go to Dash0.
- **Search**: Elasticsearch `semantic_text`, only for talks where the speaker turned on "Save transcript".
- **Telemetry**: OpenTelemetry traces, metrics and logs to Dash0. Lengths and counts only, never the text.
- **Delivery**: every change is a pull request reviewed by Gitar, built by GitHub Actions, deployed by Argo CD on a Radar cluster.

## Built and run with

Gitar by Sonar (reviews every PR, rules in `.gitar/review/`) · Dash0 (OpenTelemetry, Agent0) · Radar (Kubernetes, Argo CD, AI investigations) · Elasticsearch (semantic search) · OpenAI (translation) · Go, Kubernetes.

The day's real incidents, investigations and numbers: [DEMO.md](DEMO.md).

## Run locally

```sh
SPEAKER_TOKEN=$(openssl rand -hex 16) go run .
```

Speaker: http://localhost:8080/s/talk (Chrome). Listeners: http://localhost:8080/r/talk. Tests: `go test ./...`.

Optional: `OTEL_EXPORTER_OTLP_ENDPOINT` + `OTEL_EXPORTER_OTLP_HEADERS` (Dash0), `OPENAI_API_KEY` (translation), `ELASTIC_URL` + `ELASTIC_API_KEY` (search). Each feature stays off without its variables.

## Deploy

Push to `main` builds `ghcr.io/moezdil/hack:<sha>` and bumps `deploy/kustomization.yaml`; Argo CD applies `deploy/` (see `argocd/application.yaml`). Create Secret `hack` with the variables above first; it is never committed. One replica: fan-out is in memory.

## Privacy

No audio, no accounts. Transcripts are stored only if the speaker turns on "Save transcript" for their talk. Audio goes from the speaker's Chrome to Google's speech service.

## AI disclosure

Parts of this project were written with AI coding assistance and reviewed by the author.

## License

MIT
