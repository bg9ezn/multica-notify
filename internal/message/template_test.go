package message

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		Input: json.RawMessage(
			`{"issue":{"id":"11111111-1111-1111-1111-111111111111","number":12,"title":"Ship the thing","status":"` +
				status + `"},"status_changed":true}`),
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
	require.NoError(t, err)

	msg, err := r.Render(issueEvent("in_review"))
	require.NoError(t, err)

	assert.Contains(t, msg.Title, "#12")
	assert.Contains(t, msg.Title, "in_review")
	assert.Contains(t, msg.Body, "Ship the thing")
	assert.Equal(t, "d-42", msg.Meta["delivery_id"])
}

func TestRenderTaskFailureDistinguishesRetrying(t *testing.T) {
	r, err := NewRenderer(nil)
	require.NoError(t, err)

	terminal, err := r.Render(taskEvent(false))
	require.NoError(t, err)
	assert.Contains(t, terminal.Body, "GLM quota exhausted")

	retrying, err := r.Render(taskEvent(true))
	require.NoError(t, err)
	assert.Contains(t, retrying.Body, "will retry")
}

func TestRenderHeartbeat(t *testing.T) {
	r, err := NewRenderer(nil)
	require.NoError(t, err)

	msg, err := r.Render(event.Decode(event.Envelope{
		Trigger:    event.TriggerSchedule,
		HookKey:    "heartbeat",
		DeliveryID: "hb-1",
	}))
	require.NoError(t, err)
	assert.Contains(t, msg.Body, "hb-1")
}

func TestRenderHonorsOverrides(t *testing.T) {
	r, err := NewRenderer(map[string]config.TemplateConfig{
		SetIssueStatus: {Title: "custom {{.Issue.Status}}", Body: "b"},
	})
	require.NoError(t, err)

	msg, err := r.Render(issueEvent("in_review"))
	require.NoError(t, err)
	assert.Equal(t, "custom in_review", msg.Title)
}

func TestNewRendererRejectsUnknownSetAndBadTemplate(t *testing.T) {
	_, err := NewRenderer(map[string]config.TemplateConfig{"nope": {Title: "x", Body: "y"}})
	assert.Error(t, err, "unknown set accepted")

	_, err = NewRenderer(map[string]config.TemplateConfig{SetIssueStatus: {Title: "{{.Issue", Body: "y"}})
	assert.Error(t, err, "broken template accepted")
}
