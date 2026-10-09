// Package event decodes hook deliveries into the events multica-notify acts
// on, and owns the pipeline stages between "signed bytes arrived" and "message
// rendered": filtering, debouncing, and idempotency.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Event type strings as published by Multica's plugin event bridge
// (server/pkg/eventcontract/events.go, dot style).
const (
	EventIssueStatusChanged = "issue.status_changed"
	EventTaskCompleted      = "task.completed"
	EventTaskFailed         = "task.failed"
)

// Trigger values Multica puts on the envelope. multica-notify only ever
// receives "event" (the two event hooks) and "schedule" (the heartbeat);
// ui/manual triggers belong to hooks declared with those triggers, which this
// manifest does not have.
const (
	TriggerSchedule = "schedule"
	TriggerEvent    = "event"
)

// Actor mirrors the envelope's actor block.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Schedule carries the planned occurrence for schedule-triggered deliveries.
type Schedule struct {
	PlannedAt string `json:"planned_at"`
}

// Envelope is the JSON body Multica POSTs to a hook transport URL
// (service/plugin_hook.go hookRequestBody). Only the fields multica-notify
// consumes are declared; anything else in the payload is ignored on purpose,
// so upstream payload growth is not a breaking change (plan risk R3).
//
// Note delivery_id is SCHEDULE-only on the wire (json omitempty host-side):
// event deliveries arrive without one. Use IDKey() for idempotency, never the
// raw field.
type Envelope struct {
	Version      int             `json:"version"`
	InvocationID string          `json:"invocation_id"`
	DeliveryID   string          `json:"delivery_id"`
	Attempt      int             `json:"attempt"`
	OccurredAt   time.Time       `json:"occurred_at"`
	HookKey      string          `json:"hook_key"`
	Trigger      string          `json:"trigger"`
	EventType    string          `json:"event_type"`
	WorkspaceID  string          `json:"workspace_id"`
	Installation string          `json:"installation_id"`
	IssueID      string          `json:"issue_id"`
	Actor        Actor           `json:"actor"`
	Input        json.RawMessage `json:"input"`
	Schedule     *Schedule       `json:"schedule"`
}

// Issue is the slice of Multica's issue snapshot the notifications need
// (payload shape: {"issue": {...}, "status_changed": bool}).
type Issue struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Task is the slice of Multica's task event payload
// ({"task_id", "agent_id", "issue_id", "status", ["failure_reason",
// "retry_pending"]}).
type Task struct {
	TaskID        string `json:"task_id"`
	IssueID       string `json:"issue_id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	RetryPending  bool   `json:"retry_pending"`
}

// Event is a decoded delivery, ready for filtering and rendering.
type Event struct {
	Envelope
	Issue *Issue
	Task  *Task
}

// Decode turns the raw envelope input into the typed view for the event type.
// Unknown event types decode to an Event with no typed payload; the filter
// rejects those, but they still flow through for the webhook channel's
// "see everything" debugging use.
func Decode(env Envelope) *Event {
	e := &Event{Envelope: env}
	switch {
	case strings.HasPrefix(env.EventType, "issue."):
		var p struct {
			Issue         *Issue `json:"issue"`
			StatusChanged bool   `json:"status_changed"`
		}
		if json.Unmarshal(env.Input, &p) == nil && p.Issue != nil {
			e.Issue = p.Issue
			if e.Issue.ID == "" {
				e.Issue.ID = env.IssueID
			}
		}
	case strings.HasPrefix(env.EventType, "task."):
		var t Task
		if json.Unmarshal(env.Input, &t) == nil {
			if t.IssueID == "" {
				t.IssueID = env.IssueID
			}
			e.Task = &t
		}
	}
	return e
}

// IDKey is the idempotency identity of one logical delivery.
//
// Schedule deliveries carry a stable delivery_id across retries. Event
// deliveries do not (the field is schedule-only host-side), and their
// invocation_id changes per HTTP attempt — so the stable identity of a
// redelivered event is what happened: type, workspace, subject, occurrence
// time. Host retries of the same event reproduce those; genuinely distinct
// events do not.
func (e *Envelope) IDKey() string {
	if e.DeliveryID != "" {
		return "d:" + e.DeliveryID
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		e.EventType, e.WorkspaceID, e.Installation, e.IssueID,
		e.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	return "e:" + hex.EncodeToString(sum[:16])
}

// Ref renders a short human reference for the event's subject: "#12" when the
// issue snapshot carries a number, a truncated id otherwise.
func (e *Event) Ref() string {
	if e.Issue != nil {
		if e.Issue.Number > 0 {
			return "#" + strconv.Itoa(e.Issue.Number)
		}
		return shortID(e.Issue.ID)
	}
	if e.Task != nil {
		return shortID(e.Task.TaskID)
	}
	return shortID(e.DeliveryID)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
