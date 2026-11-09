# Readalong: live captions for every talk

Scan a QR code, read the talk on your phone. No app, no account, nothing stored.

![Listener page filling with captions](docs/demo.gif)

Built solo at the Cloud Native Hackathon (Salt Lake City, Nov 9 2026). Most sessions have no captions; deaf and hard-of-hearing attendees, non-native speakers and the back row lose the talk. Readalong gives any talk live captions in under a minute.

Live: https://hack.cnh.radarhq.io

## How it works

```
Speaker's laptop (Chrome)            Go server on Kubernetes             Listeners' phones
speech recognition in the browser -> POST /api/rooms/{room}/captions -> GET /api/rooms/{room}/stream (SSE)
                                      validates, fans out in memory,       current line + last lines,
                                      keeps last 50 finals for late joiners text size, high contrast
```

- **Speaker page** `/s/{room}`: Chrome's built-in speech recognition (`webkitSpeechRecognition`) turns speech into text and sends interim and final results with a speaker token. Chrome stops recognition on its own after silence; the page restarts it unless you pressed Pause or Stop.
- **Server**: one Go binary, standard library HTTP. Validates room (`^[a-z0-9-]{1,32}$`), caption length (500 chars) and the token; fans out over server-sent events; never blocks on a slow phone (bounded per-listener queues); pings every 15 s so proxies keep the stream open; on shutdown tells listeners to reconnect.
- **Listener page** `/r/{room}`: finished lines plus the current one, auto-scroll with a pause toggle, text size, high contrast, `aria-live` region for screen readers, and a "last caption N s ago" badge.
- **Telemetry**: OpenTelemetry traces, metrics and logs to Dash0. Caption text never enters telemetry, only lengths and counts.

## Try it in 2 minutes (no install)

1. Open https://hack.cnh.radarhq.io/r/demo on your phone. That is the listener view.
2. Ask the speaker (whoever has the token) to open https://hack.cnh.radarhq.io/s/demo in Chrome, paste the token, press **Start captions** and talk.
3. Watch the lines appear. Tap **A+** for bigger text, **High contrast** to flip colors, **Pause scroll** to re-read.

To run a talk of your own, pick any room name (letters, digits, dashes) and put the listener link or a QR code of it on your first slide: `qrencode -o qr.png https://hack.cnh.radarhq.io/r/<room>`. The one for room `talk` is in [docs/qr-talk.png](docs/qr-talk.png):

<img src="docs/qr-talk.png" alt="QR code for https://hack.cnh.radarhq.io/r/talk" width="180">

## Run it yourself

Needs Go 1.22+ and Chrome for the speaker page.

```sh
git clone https://github.com/moezdil/hack && cd hack
SPEAKER_TOKEN=$(openssl rand -hex 16) go run .
```

- Speaker: http://localhost:8080/s/talk in Chrome, paste the token (`echo $SPEAKER_TOKEN`), press Start, allow the microphone.
- Listeners: http://localhost:8080/r/talk on any device on the same network (use your laptop's LAN IP instead of localhost).
- Tests: `go test -race ./...` (on macOS with a broken cgo toolchain: `CGO_ENABLED=0 go test ./...`).

Optional, to send telemetry to Dash0:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=https://ingress.<region>.aws.dash0.com \
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>,Dash0-Dataset=default" \
SPEAKER_TOKEN=... go run .
```

With no endpoint set nothing is exported.

## Deploy on Kubernetes

Every push to `main` builds `ghcr.io/moezdil/hack:<sha>` and bumps the tag in `deploy/kustomization.yaml`; Argo CD deploys `deploy/` to namespace `team-hack`.

1. Create the secret once (never commit it):
   ```sh
   kubectl -n team-hack create secret generic hack \
     --from-literal=SPEAKER_TOKEN=$(openssl rand -hex 16) \
     --from-literal=OTEL_EXPORTER_OTLP_ENDPOINT=... \
     --from-literal=OTEL_EXPORTER_OTLP_HEADERS=...
   ```
2. Apply the Argo CD Application: `kubectl apply -f argocd/application.yaml`.
3. Without Argo CD: `kubectl apply -k deploy/`.

One replica only: the fan-out is in memory. More replicas need a shared pub/sub, which is a known ceiling.

## Development workflow

Every change is a small pull request. Gitar reviews each one against `.gitar/review/hack.md` (privacy of caption text, bounded channels, token and input validation, accessibility rules, manifests with limits and probes). CI runs `gofmt`, `go vet` and `go test -race`. `DEMO.md` is the running log of the day.

## Privacy

Nothing is recorded or stored: no audio, no transcripts, no accounts. Audio goes from the speaker's Chrome to Google's speech service for recognition. Listeners send nothing but the room name.

## Limits

- Chrome only for the speaker (Web Speech API). Listeners can use any browser.
- English recognition (`en-US`); translation is the next step.
- The delay badge uses device clocks; a few hundred ms of skew is normal.

## AI disclosure

Parts of this project were written with AI coding assistance and reviewed by the author.

## License

MIT
