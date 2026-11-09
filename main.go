package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"
	"unicode/utf8"
)

//go:embed web
var webFS embed.FS

var roomRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

const maxText = 500

type server struct {
	hub     *hub
	token   string
	closing chan struct{} // closed on shutdown: streams send "bye" and return
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms/{room}/captions", s.postCaption)
	mux.HandleFunc("GET /api/rooms/{room}/stream", s.stream)
	mux.HandleFunc("GET /s/{room}", s.page("web/speaker.html"))
	mux.HandleFunc("GET /r/{room}", s.page("web/listener.html"))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-s.closing:
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
		default:
			w.Write([]byte("ok"))
		}
	})
	return mux
}

func (s *server) page(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !roomRe.MatchString(r.PathValue("room")) {
			http.Error(w, "bad room", http.StatusBadRequest)
			return
		}
		b, err := webFS.ReadFile(file)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}

func (s *server) authorized(r *http.Request) bool {
	want := "Bearer " + s.token
	got := r.Header.Get("Authorization")
	return s.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *server) postCaption(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	room := r.PathValue("room")
	if !roomRe.MatchString(room) {
		http.Error(w, "bad room", http.StatusBadRequest)
		return
	}
	var c caption
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(c.Text) > maxText {
		http.Error(w, "text too long", http.StatusBadRequest)
		return
	}
	listeners, dropped := s.hub.publish(room, c)
	slog.Info("caption", "room", room, "seq", c.Seq, "final", c.Final, "len", len(c.Text),
		"listeners", listeners, "dropped", dropped, "receivedDelayMs", time.Now().UnixMilli()-c.SpokenAt)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"listeners": listeners})
}

func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("room")
	if !roomRe.MatchString(room) {
		http.Error(w, "bad room", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, backlog, leave := s.hub.subscribe(room)
	defer leave()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	send("backlog", backlog)

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closing:
			send("bye", nil)
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case c := <-ch:
			send("caption", c)
		}
	}
}

func main() {
	token := os.Getenv("SPEAKER_TOKEN")
	if token == "" {
		slog.Error("SPEAKER_TOKEN is required")
		os.Exit(1)
	}
	addr := ":" + envOr("PORT", "8080")
	s := &server{hub: newHub(), token: token, closing: make(chan struct{})}
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()

	close(s.closing) // readyz goes 503, streams say bye so listeners reconnect elsewhere
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
