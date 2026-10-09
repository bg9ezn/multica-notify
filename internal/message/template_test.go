package message

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/event"
)

func issueEvent(status string) *event.Event {
	return event.Decode(event.Envelope{
		HookKey:    "issue_status",
		Trigger:    event.TriggerEvent,
		EventType:  event.EventIssueStatusChanged,
		DeliveryID: "d-42",
		IssueID:    "11111111-1111-1111-1111-111111111111",
		Input:      json.RawMessage(`{"issue":{"id":"11111111-1111-1111-1111-111111111111","number":12,"title":"Ship the thing","status":"` + status + `"},"status_changed":true}`),
	})
}

func taskEvent(retryPending bool) *event.Event {
	return event.Decode(event.Envelope{
		HookKey:    "task_lifecycle",
		Trigger:    event.TriggerEvent,
		EventType:  event.EventTaskFailed,
		DeliveryID: "d-43",
		IssueID:    "i-9",
		Input:      json.RawMessage(`{"task_id":"t-9abc1234-rest","issue_id":"i-9","status":"failed","failure_reason":"GLM quota exhausted","retry_pending":` + boolJSON(retryPending) + `}`),
	})
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestRenderIssueStatus(t *testing.T) {
	r, err := NewRenderer(nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := r.Render(issueEvent("in_review"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Title, "#12") || !strings.Contains(msg.Title, "in_review") {
		t.Errorf("title = %q", msg.Title)
	}
	if !strings.Contains(msg.Body, "Ship the thing") {
		t.Errorf("body = %q", msg.Body)
	}
	if msg.Meta["delivery_id"] != "d-42" {
		t.Errorf("meta = %v", msg.Meta)
	}
}

func TestRenderTaskFailureDistinguishesRetrying(t *testing.T) {
	r, _ := NewRenderer(nil)

	terminal, err := r.Render(taskEvent(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(terminal.Body, "GLM quota exhausted") {
		t.Errorf("terminal body = %q", terminal.Body)
	}

	retrying, err := r.Render(taskEvent(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retrying.Body, "will retry") {
		t.Errorf("retrying body = %q", retrying.Body)
	}
}

func TestRenderHeartbeat(t *testing.T) {
	r, _ := NewRenderer(nil)
	e := event.Decode(event.Envelope{
		Trigger:    event.TriggerSchedule,
		HookKey:    "heartbeat",
		DeliveryID: "hb-1",
	})
	msg, err := r.Render(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Body, "hb-1") {
		t.Errorf("body = %q", msg.Body)
	}
}

func TestRenderHonorsOverrides(t *testing.T) {
	r, err := NewRenderer(map[string]config.TemplateConfig{
		SetIssueStatus: {Title: "custom {{.Issue.Status}}", Body: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := r.Render(issueEvent("in_review"))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Title != "custom in_review" {
		t.Errorf("title = %q", msg.Title)
	}
}

func TestNewRendererRejectsUnknownSetAndBadTemplate(t *testing.T) {
	if _, err := NewRenderer(map[string]config.TemplateConfig{"nope": {Title: "x", Body: "y"}}); err == nil {
		t.Error("unknown set accepted")
	}
	if _, err := NewRenderer(map[string]config.TemplateConfig{SetIssueStatus: {Title: "{{.Issue", Body: "y"}}); err == nil {
		t.Error("broken template accepted")
	}
}
