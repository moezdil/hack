# hack

Live captions for any conference talk. Scan a QR code, read along on your phone: no account, no app, no sign-up.

Built for the Cloud Native Hackathon (Salt Lake City, Nov 9 2026). Conferences are hard to follow for deaf and hard-of-hearing attendees and non-native speakers; this makes a talk readable in real time.

## How it works

Speaker page (Chrome speech recognition) -> Go server -> server-sent events -> listener phones. Nothing is stored; audio goes to the browser's speech service.

## Setup

Run locally (Go 1.22+):

```sh
SPEAKER_TOKEN=<pick-a-secret> go run .
```

- Speaker: open `http://localhost:8080/s/<room>` in Chrome, enter the token, press Start.
- Listeners: open `http://localhost:8080/r/<room>` on any device.
- Room names are `a-z`, `0-9` and `-`, up to 32 characters.

Tests: `go test -race ./...`

## AI disclosure

Parts of this project were written with AI coding assistance and reviewed by the author.

## License

MIT
