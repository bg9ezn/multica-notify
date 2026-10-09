package event

import (
	"sync"
	"time"
)

// Debouncer collapses rapid state flips on the same subject into one
// notification carrying the LATEST state, sent when the window closes —
// "delayed latest", not "first + suppress". An issue that flips
// todo -> in_progress -> in_review inside the window notifies once, as
// in_review, when the window closes.
type Debouncer struct {
	window time.Duration

	mu      sync.Mutex
	pending map[string]func()
	timers  map[string]*time.Timer
}

// NewDebouncer builds a debouncer collapsing events per key within window.
func NewDebouncer(window time.Duration) *Debouncer {
	return &Debouncer{
		window:  window,
		pending: make(map[string]func()),
		timers:  make(map[string]*time.Timer),
	}
}

// Submit registers (or replaces) the emit function for key and (re)arms the
// window timer. The emit registered last wins; earlier ones are dropped
// unexecuted.
func (d *Debouncer) Submit(key string, emit func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[key] = emit
	if _, armed := d.timers[key]; armed {
		return
	}
	d.timers[key] = time.AfterFunc(d.window, func() { d.fire(key) })
}

// fire sends the latest emit for key when its window closes.
func (d *Debouncer) fire(key string) {
	d.mu.Lock()
	emit, ok := d.pending[key]
	delete(d.pending, key)
	delete(d.timers, key)
	d.mu.Unlock()
	if ok {
		emit()
	}
}

// Flush immediately fires every pending emit. Used on shutdown so a window
// that would have closed seconds later does not swallow its notification.
func (d *Debouncer) Flush() {
	d.mu.Lock()
	pending := d.pending
	timers := d.timers
	d.pending = make(map[string]func())
	d.timers = make(map[string]*time.Timer)
	d.mu.Unlock()
	for _, t := range timers {
		t.Stop()
	}
	for _, emit := range pending {
		emit()
	}
}
