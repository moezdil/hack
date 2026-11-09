package main

import "sync"

// caption is one speech recognition result. SpokenAt is the speaker browser's clock in ms.
type caption struct {
	Seq      int    `json:"seq"`
	Text     string `json:"text"`
	Final    bool   `json:"final"`
	SpokenAt int64  `json:"spokenAt"`
	Save     bool   `json:"save,omitempty"` // speaker opted in to a searchable transcript
	Lang     string `json:"lang,omitempty"` // set on translated finals; empty means the original
}

const (
	backlogSize = 50 // finals kept per room for late joiners
	listenerBuf = 32 // per-listener queue
)

type room struct {
	finals    []caption
	listeners map[chan caption]string // channel -> language wanted ("" = original only)
}

// hub fans captions out to listeners, per room, in memory.
// ponytail: one replica only; more replicas need a shared pub/sub (e.g. Redis).
type hub struct {
	mu    sync.Mutex
	rooms map[string]*room
}

func newHub() *hub { return &hub{rooms: map[string]*room{}} }

func (h *hub) room(name string) *room {
	r := h.rooms[name]
	if r == nil {
		r = &room{listeners: map[chan caption]string{}}
		h.rooms[name] = r
	}
	return r
}

// subscribe returns the listener's channel, a copy of the recent finals, and a func to leave.
// lang is the translation the listener wants on top of the original ("" for none).
func (h *hub) subscribe(name, lang string) (<-chan caption, []caption, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.room(name)
	ch := make(chan caption, listenerBuf)
	r.listeners[ch] = lang
	backlog := append([]caption(nil), r.finals...)
	leave := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(r.listeners, ch)
		if len(r.listeners) == 0 && len(r.finals) == 0 {
			delete(h.rooms, name)
		}
	}
	return ch, backlog, leave
}

// publish never blocks. A full listener loses an interim update (the next one replaces it anyway);
// for a final, its oldest queued update is evicted to make room.
// A translated caption (c.Lang != "") goes only to listeners who asked for that language.
func (h *hub) publish(name string, c caption) (listeners, dropped int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.room(name)
	if c.Final && c.Lang == "" {
		r.finals = append(r.finals, c)
		if len(r.finals) > backlogSize {
			r.finals = r.finals[len(r.finals)-backlogSize:]
		}
	}
	for ch, want := range r.listeners {
		if c.Lang != "" && want != c.Lang {
			continue
		}
		select {
		case ch <- c:
			continue
		default:
		}
		dropped++
		if !c.Final {
			continue
		}
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- c:
		default:
		}
	}
	return len(r.listeners), dropped
}

// langs returns the distinct translation languages listeners of a room currently want.
func (h *hub) langs(name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	if r := h.rooms[name]; r != nil {
		for _, l := range r.listeners {
			if l != "" && !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

func (h *hub) counts() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := make(map[string]int, len(h.rooms))
	for name, r := range h.rooms {
		m[name] = len(r.listeners)
	}
	return m
}

func (h *hub) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[name]; r != nil {
		return len(r.listeners)
	}
	return 0
}
