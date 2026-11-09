package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// searcher stores final captions in Elasticsearch, only for rooms whose speaker
// turned "save transcript" on. The index uses semantic_text, so a listener's
// question finds lines by meaning, not just words.
type searcher struct {
	url, key, index string
	client          *http.Client
	tel             *telemetry
}

func newSearcher(tel *telemetry) *searcher {
	url := strings.TrimRight(os.Getenv("ELASTIC_URL"), "/")
	key := os.Getenv("ELASTIC_API_KEY")
	if url == "" || key == "" {
		return nil
	}
	s := &searcher{url: url, key: key, index: "captions", client: &http.Client{Timeout: 10 * time.Second}, tel: tel}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.ensureIndex(ctx); err != nil {
		slog.Warn("elasticsearch index", "err", err)
	}
	return s
}

func (s *searcher) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.url+path, &buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "ApiKey "+s.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, b, nil
}

func (s *searcher) ensureIndex(ctx context.Context) error {
	code, _, err := s.do(ctx, "HEAD", "/"+s.index, nil)
	if err != nil || code == 200 {
		return err
	}
	code, b, err := s.do(ctx, "PUT", "/"+s.index, map[string]any{
		"mappings": map[string]any{"properties": map[string]any{
			"room":     map[string]any{"type": "keyword"},
			"seq":      map[string]any{"type": "long"},
			"text":     map[string]any{"type": "semantic_text"},
			"spokenAt": map[string]any{"type": "date"},
		}},
	})
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("create index: %d %s", code, b)
	}
	return nil
}

// store indexes one final caption. It runs in its own goroutine so the caption
// path never waits on Elasticsearch.
func (s *searcher) store(room string, c caption) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, span := s.tel.tracer.Start(ctx, "search.store")
	defer span.End()
	span.SetAttributes(attribute.String("room", room), attribute.Int("seq", c.Seq))
	code, b, err := s.do(ctx, "PUT", fmt.Sprintf("/%s/_doc/%s-%d", s.index, room, c.Seq),
		map[string]any{"room": room, "seq": c.Seq, "text": c.Text, "spokenAt": c.SpokenAt})
	if err == nil && code >= 300 {
		err = fmt.Errorf("status %d: %s", code, b)
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		slog.WarnContext(ctx, "search store", "room", room, "seq", c.Seq, "err", err)
	}
}

type hit struct {
	Seq      int    `json:"seq"`
	Text     string `json:"text"`
	SpokenAt int64  `json:"spokenAt"`
}

// search returns the best matching lines of a room for a free-text question.
func (s *searcher) search(ctx context.Context, room, q string) ([]hit, error) {
	ctx, span := s.tel.tracer.Start(ctx, "search.query")
	defer span.End()
	span.SetAttributes(attribute.String("room", room), attribute.Int("query.length", len(q)))
	code, b, err := s.do(ctx, "POST", "/"+s.index+"/_search", map[string]any{
		"size": 10,
		"query": map[string]any{"bool": map[string]any{
			"filter": []any{map[string]any{"term": map[string]any{"room": room}}},
			"must":   []any{map[string]any{"semantic": map[string]any{"field": "text", "query": q}}},
		}},
		"_source": []string{"seq", "spokenAt"},
		"fields":  []string{"text"},
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if code >= 300 {
		span.SetStatus(codes.Error, "search failed")
		return nil, fmt.Errorf("search: %d %s", code, b)
	}
	var res struct {
		Hits struct {
			Hits []struct {
				Source hit                 `json:"_source"`
				Fields map[string][]string `json:"fields"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	out := make([]hit, 0, len(res.Hits.Hits))
	for _, h := range res.Hits.Hits {
		t := h.Source
		if v := h.Fields["text"]; len(v) > 0 {
			t.Text = v[0]
		}
		out = append(out, t)
	}
	span.SetAttributes(attribute.Int("hits", len(out)))
	return out, nil
}
