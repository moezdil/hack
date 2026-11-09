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
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

//go:embed web
var webFS embed.FS

var (
	roomRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	langRe = regexp.MustCompile(`^$|^[a-z]{2}(-[A-Za-z]{2,4})?$`)
)

const maxText = 500

type server struct {
	hub     *hub
	tel     *telemetry
	search  *searcher   // nil when Elasticsearch is not configured
	trans   *translator // nil when OPENAI_API_KEY is not set
	token   string
	closing chan struct{} // closed on shutdown: streams send "bye" and return
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms/{room}/captions", s.postCaption)
	mux.HandleFunc("GET /api/rooms/{room}/stream", s.stream)
	mux.HandleFunc("GET /api/rooms/{room}/search", s.searchHandler)
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
	room := r.PathValue("room")
	ctx, span := s.tel.tracer.Start(r.Context(), "caption.ingest", trace.WithAttributes(attribute.String("room", room)))
	defer span.End()
	var c caption
	reject := func(status int, reason string) {
		span.SetStatus(codes.Error, reason)
		s.tel.captions.Add(ctx, 1, metric.WithAttributes(attribute.String("status", reason)))
		http.Error(w, reason, status)
	}
	if !s.authorized(r) {
		reject(http.StatusUnauthorized, "unauthorized")
		return
	}
	if !roomRe.MatchString(room) {
		reject(http.StatusBadRequest, "bad room")
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c); err != nil {
		reject(http.StatusBadRequest, "bad body")
		return
	}
	if utf8.RuneCountInString(c.Text) > maxText {
		reject(http.StatusBadRequest, "text too long")
		return
	}
	if c.From != "" && !langRe.MatchString(c.From) {
		reject(http.StatusBadRequest, "bad from")
		return
	}
	// Never the caption text itself in telemetry, only its length.
	span.SetAttributes(attribute.Int("seq", c.Seq), attribute.Bool("final", c.Final), attribute.Int("text.length", len(c.Text)))
	s.tel.recordDelay(ctx, room, "received", time.Now().UnixMilli()-c.SpokenAt)

	_, bspan := s.tel.tracer.Start(ctx, "caption.broadcast")
	listeners, dropped := s.hub.publish(room, c)
	bspan.SetAttributes(attribute.Int("listeners", listeners), attribute.Int("dropped", dropped))
	bspan.End()

	if c.Final && c.Save && s.search != nil {
		go s.search.store(room, c)
	}
	if c.Final && s.trans != nil {
		if langs := s.hub.langs(room); len(langs) > 0 {
			go s.translateAndPublish(room, c, langs)
		}
	}
	s.tel.captions.Add(ctx, 1, metric.WithAttributes(attribute.Bool("final", c.Final), attribute.String("status", "ok")))
	slog.InfoContext(ctx, "caption", "room", room, "seq", c.Seq, "final", c.Final, "len", len(c.Text),
		"listeners", listeners, "dropped", dropped)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"listeners": listeners})
}

// searchHandler answers {"enabled":bool} without q, and the matching lines with q.
func (s *server) searchHandler(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("room")
	if !roomRe.MatchString(room) {
		http.Error(w, "bad room", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		json.NewEncoder(w).Encode(map[string]bool{"enabled": s.search != nil, "translate": s.trans != nil})
		return
	}
	if s.search == nil {
		http.Error(w, "search not configured", http.StatusNotImplemented)
		return
	}
	if utf8.RuneCountInString(q) > 200 {
		http.Error(w, "query too long", http.StatusBadRequest)
		return
	}
	hits, err := s.search.search(r.Context(), room, q)
	if err != nil {
		slog.WarnContext(r.Context(), "search", "room", room, "err", err)
		http.Error(w, "search failed", http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"hits": hits})
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
	lang := r.URL.Query().Get("lang")
	if !langRe.MatchString(lang) || s.trans == nil {
		lang = ""
	}
	ch, backlog, leave := s.hub.subscribe(room, lang)
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
	// EventSource sends Last-Event-ID only when it reconnects after a drop.
	if r.Header.Get("Last-Event-ID") != "" {
		s.tel.reconnects.Add(r.Context(), 1, metric.WithAttributes(attribute.String("room", room)))
	}

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
			fmt.Fprintf(w, "id: %d\n", c.Seq)
			send("caption", c)
			s.tel.recordDelay(r.Context(), room, "sent", time.Now().UnixMilli()-c.SpokenAt)
		}
	}
}

func main() {
	token := os.Getenv("SPEAKER_TOKEN")
	if token == "" {
		slog.Error("SPEAKER_TOKEN is required")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTel, err := setupTelemetry(ctx)
	if err != nil {
		slog.Error("telemetry", "err", err)
		os.Exit(1)
	}
	addr := ":" + envOr("PORT", "8080")
	s := newServer(token)
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}

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
	if err := shutdownTel(shutdownCtx); err != nil {
		slog.Error("telemetry shutdown", "err", err)
	}
}

func newServer(token string) *server {
	h := newHub()
	tel := newTelemetry(h)
	return &server{hub: h, tel: tel, search: newSearcher(tel), trans: newTranslator(tel), token: token, closing: make(chan struct{})}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
