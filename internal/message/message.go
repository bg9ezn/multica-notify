// Package message renders decoded events into the title/body pairs the
// notification channels consume.
package message

// Message is one rendered notification. Type is the notify type
// (info|success|warning|error) — apprise passes it to the receiver, ntfy
// maps it to priority/tags, webhook includes it. Meta carries the event
// identity so metadata-consuming channels can include it.
type Message struct {
	Title string
	Body  string
	Type  string
	Meta  map[string]string
}
