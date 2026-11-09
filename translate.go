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
	"go.opentelemetry.io/otel/metric"
)

// translator turns final captions into the languages listeners asked for, through the OpenAI API.
type translator struct {
	url, key, model string
	client          *http.Client
	tel             *telemetry
	cost            metric.Float64Counter
}

// Approximate list prices per token, used only for the cost counter in Dash0.
const (
	priceIn  = 0.15 / 1e6
	priceOut = 0.60 / 1e6
)

func newTranslator(tel *telemetry) *translator {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil
	}
	t := &translator{url: envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"), key: key,
		model: envOr("OPENAI_MODEL", "gpt-4o-mini"), client: &http.Client{Timeout: 15 * time.Second}, tel: tel}
	t.cost, _ = tel.meter.Float64Counter("llm.cost.usd", metric.WithDescription("Estimated spend on translation"))
	return t
}

// translate returns text in lang. One call per caption and language, no batching.
// ponytail: sequential per language; parallel calls plus a per-caption cache if latency shows up in Dash0.
func (t *translator) translate(ctx context.Context, text, lang string) (string, error) {
	ctx, span := t.tel.tracer.Start(ctx, "translate")
	defer span.End()
	span.SetAttributes(attribute.String("gen_ai.system", "openai"), attribute.String("gen_ai.request.model", t.model),
		attribute.String("lang", lang), attribute.Int("text.length", len(text)))
	body := map[string]any{
		"model":       t.model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": "Translate the conference caption into the language with IETF tag " + lang +
				". Keep technical terms, product names and acronyms (Kubernetes, kubelet, eBPF, Argo CD, OpenTelemetry) as they are. Reply with the translation only."},
			{"role": "user", "content": text},
		},
	}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req, err := http.NewRequestWithContext(ctx, "POST", t.url+"/chat/completions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+t.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := t.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		span.SetStatus(codes.Error, fmt.Sprintf("status %d", res.StatusCode))
		return "", fmt.Errorf("openai: %d %s", res.StatusCode, b)
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
		Usage struct {
			In  int `json:"prompt_tokens"`
			Out int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		span.SetStatus(codes.Error, "bad response")
		return "", fmt.Errorf("openai: bad response")
	}
	span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", out.Usage.In), attribute.Int("gen_ai.usage.output_tokens", out.Usage.Out))
	t.cost.Add(ctx, float64(out.Usage.In)*priceIn+float64(out.Usage.Out)*priceOut, metric.WithAttributes(attribute.String("lang", lang)))
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// translateAndPublish runs off the request path: one caption, every language listeners want.
func (s *server) translateAndPublish(room string, c caption, langs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, lang := range langs {
		text, err := s.trans.translate(ctx, c.Text, lang)
		if err != nil {
			slog.WarnContext(ctx, "translate", "room", room, "seq", c.Seq, "lang", lang, "err", err)
			continue
		}
		s.hub.publish(room, caption{Seq: c.Seq, Text: text, Final: true, SpokenAt: c.SpokenAt, Lang: lang})
	}
}
