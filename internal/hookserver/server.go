package hookserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/message"
)

const (
	maxBodyBytes = 1 << 20

	// The hook answered 200 as soon as the delivery is accepted; sends happen
	// on this pool. Bounded like the host's own dispatcher: a slow receiver
	// downstream must degrade by dropping (logged), never by growing memory.
	workerCount = 8
	queueDepth  = 256
)

// Deps is the mutable half of the handler, rebuilt atomically on SIGHUP.
type Deps struct {
	// Muted is the master switch (config top-level enabled: false).
	// Deliveries are still verified, journaled and filtered; fan-out is
	// suppressed. Channel state stays built so unmuting needs no rebuild.
	Muted     bool
	Filter    *event.Filter
	Journal   *event.Journal
	Debouncer *event.Debouncer
	Renderer  *message.Renderer
	Channels  []channel.Channel
	Logger    *slog.Logger
}

// dispatch fans one accepted event out to every channel.
func (d *Deps) dispatch(ctx context.Context, e *event.Event) {
	if d.Muted {
		d.Logger.Debug("notification suppressed (muted)", "event_type", e.EventType, "subject", e.Ref())
		return
	}
	msg, err := d.Renderer.Render(e)
	if err != nil {
		d.Logger.Error("render notification", "event_type", e.EventType, "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, ch := range d.Channels {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ch.Send(ctx, msg); err != nil {
				d.Logger.Warn("channel send failed", "channel", ch.Name(), "error", err)
			} else {
				d.Logger.Info("notification sent", "channel", ch.Name(), "title", msg.Title)
			}
		}()
	}
	wg.Wait()
}

// Handler is the plugin author's side of the hook contract: an ordinary HTTP
// server that Multica only ever hands a signed POST.
type Handler struct {
	verifier *Verifier
	deps     atomic.Pointer[Deps]

	jobs chan func(context.Context)
	wg   sync.WaitGroup
}

// NewHandler starts the internal send pool.
func NewHandler(verifier *Verifier, deps *Deps) *Handler {
	h := &Handler{
		verifier: verifier,
		jobs:     make(chan func(context.Context), queueDepth),
	}
	h.deps.Store(deps)
	for i := 0; i < workerCount; i++ {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			ctx := context.Background()
			for job := range h.jobs {
				job(ctx)
			}
		}()
	}
	return h
}

// UpdateDeps atomically swaps configuration-dependent state (filter, debounce
// window, channels, templates). Used by SIGHUP reload.
func (h *Handler) UpdateDeps(deps *Deps) { h.deps.Store(deps) }

// Close drains the send pool. Pending debounce windows are flushed by the
// caller (they own the debouncer lifecycle).
func (h *Handler) Close(timeout time.Duration) {
	close(h.jobs)
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	case r.Method != http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case !strings.HasPrefix(r.URL.Path, "/hooks/"):
		http.NotFound(w, r)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusRequestEntityTooLarge)
		return
	}

	d := h.loadDeps()
	if err := h.verifier.Verify(raw, r.Header.Get("x-multica-timestamp"), r.Header.Get("x-multica-signature"), time.Now()); err != nil {
		d.Logger.Warn("rejected delivery", "error", err, "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}

	var env event.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("parse envelope: %v", err)})
		return
	}

	// Idempotency: a host retry of the same logical delivery must not notify
	// twice. Schedule deliveries repeat their delivery_id; event deliveries
	// arrive without one, so the identity falls back to the composite event
	// key (see event.Envelope.IDKey).
	idemKey := env.IDKey()
	if d.Journal.Seen(idemKey) {
		writeJSON(w, http.StatusOK, map[string]any{"received": idemKey, "duplicate": true})
		return
	}
	if err := d.Journal.Record(idemKey); err != nil {
		d.Logger.Warn("journal record failed; continuing with degraded dedupe", "error", err)
	}

	e := event.Decode(env)
	if !d.Filter.Allow(e) {
		d.Logger.Debug("delivery filtered", "event_type", e.EventType, "reason", d.Filter.SkipReason(e))
		writeJSON(w, http.StatusOK, map[string]any{"received": idemKey, "skipped": d.Filter.SkipReason(e)})
		return
	}

	accepted := false
	select {
	case h.jobs <- func(ctx context.Context) { d.deliver(ctx, e) }:
		accepted = true
	default:
		// Bounded queue full: drop like the host's dispatcher does, loudly.
		d.Logger.Error("send queue full, dropping delivery", "idem_key", idemKey)
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": idemKey, "queued": accepted})
}

// deliver routes one accepted event: issue status through the debounce
// window, everything else straight to the channels.
func (d *Deps) deliver(ctx context.Context, e *event.Event) {
	if d.Debouncer != nil && e.Trigger != event.TriggerSchedule && e.EventType == event.EventIssueStatusChanged {
		key := "issue:" + e.Ref()
		d.Debouncer.Submit(key, func() {
			// The window closed; render from the LATEST event submitted for
			// this key. Submit replaced the closure, so e is the newest.
			d.dispatch(ctx, e)
		})
		return
	}
	d.dispatch(ctx, e)
}

func (h *Handler) loadDeps() *Deps {
	return h.deps.Load()
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
