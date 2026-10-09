// Package message renders decoded events into the title/body pairs the
// notification channels consume.
package message

// Message is one rendered notification. Meta carries the event identity so
// metadata-consuming channels (webhook) can include it; visual channels
// (apprise, ntfy) ignore it.
type Message struct {
	Title string
	Body  string
	Meta  map[string]string
}
