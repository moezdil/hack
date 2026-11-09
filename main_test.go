package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlowListenerDoesNotBlock(t *testing.T) {
	h := newHub()
	slow, _, leaveSlow := h.subscribe("r") // never read
	defer leaveSlow()
	fast, _, leaveFast := h.subscribe("r")
	defer leaveFast()

	done := make(chan struct{})
	go func() {
		for i := 0; i < listenerBuf*4; i++ {
			h.publish("r", caption{Seq: i, Text: "x", Final: true})
			<-fast
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow listener")
	}
	if got := len(slow); got != listenerBuf {
		t.Fatalf("slow queue = %d, want %d", got, listenerBuf)
	}
	if last := drain(slow); last.Seq != listenerBuf*4-1 {
		t.Fatalf("slow listener's newest final = %d, want the latest", last.Seq)
	}
}

func drain(ch <-chan caption) (last caption) {
	for len(ch) > 0 {
		last = <-ch
	}
	return last
}

func TestBacklogKeepsLastFinals(t *testing.T) {
	h := newHub()
	for i := 0; i < backlogSize+10; i++ {
		h.publish("r", caption{Seq: i, Final: true})
	}
	h.publish("r", caption{Seq: 999, Final: false})
	_, backlog, leave := h.subscribe("r")
	defer leave()
	if len(backlog) != backlogSize || backlog[0].Seq != 10 {
		t.Fatalf("backlog len %d first %d, want %d first 10", len(backlog), backlog[0].Seq, backlogSize)
	}
}

func TestStreamCancelRemovesListener(t *testing.T) {
	s := &server{hub: newHub(), token: "t", closing: make(chan struct{})}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/rooms/r/stream", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(res.Body).ReadString('\n')
	if line != "event: backlog\n" {
		t.Fatalf("first line %q", line)
	}
	if s.hub.count("r") != 1 {
		t.Fatal("listener not registered")
	}
	cancel()
	res.Body.Close()
	waitFor(t, func() bool { return s.hub.count("r") == 0 })
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if len(s.hub.rooms) != 0 {
		t.Fatal("empty room not removed")
	}
}

func TestShutdownSendsBye(t *testing.T) {
	s := &server{hub: newHub(), token: "t", closing: make(chan struct{})}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/rooms/r/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	waitFor(t, func() bool { return s.hub.count("r") == 1 })
	close(s.closing)
	body := new(strings.Builder)
	bufio.NewReader(res.Body).WriteTo(body)
	if !strings.Contains(body.String(), "event: bye") {
		t.Fatalf("no bye in %q", body)
	}
}

func TestPostCaptionValidation(t *testing.T) {
	s := &server{hub: newHub(), token: "secret", closing: make(chan struct{})}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	cases := []struct {
		name, room, auth, body string
		want                   int
	}{
		{"ok", "talk-1", "Bearer secret", `{"seq":1,"text":"hi","final":true}`, 200},
		{"missing token", "talk-1", "", `{"text":"hi"}`, 401},
		{"wrong token", "talk-1", "Bearer nope", `{"text":"hi"}`, 401},
		{"bad room", "Talk_1", "Bearer secret", `{"text":"hi"}`, 400},
		{"too long", "talk-1", "Bearer secret", `{"text":"` + strings.Repeat("a", maxText+1) + `"}`, 400},
		{"bad json", "talk-1", "Bearer secret", `{`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", ts.URL+"/api/rooms/"+c.room+"/captions", strings.NewReader(c.body))
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != c.want {
				t.Fatalf("status %d, want %d", res.StatusCode, c.want)
			}
		})
	}
}

func TestPages(t *testing.T) {
	s := &server{hub: newHub(), token: "t", closing: make(chan struct{})}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()
	for path, want := range map[string]int{"/": 200, "/s/talk": 200, "/r/talk": 200, "/r/Bad_Room": 400, "/nope": 404} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: status %d, want %d", path, res.StatusCode, want)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not met in time")
}
