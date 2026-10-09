package event

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envelope(eventType, input string) *Event {
	return Decode(Envelope{
		HookKey:    "issue_status",
		Trigger:    TriggerEvent,
		EventType:  eventType,
		DeliveryID: "d-1",
		IssueID:    "11111111-1111-1111-1111-111111111111",
		Input:      json.RawMessage(input),
	})
}

func TestDecodeIssueSnapshot(t *testing.T) {
	e := envelope(EventIssueStatusChanged, `{"issue":{"id":"i-1","number":12,"title":"T","status":"in_review"},"status_changed":true}`)
	require.NotNil(t, e.Issue, "issue not decoded")
	assert.Equal(t, "in_review", e.Issue.Status)
	assert.Equal(t, 12, e.Issue.Number)
	assert.Equal(t, "#12", e.Ref())
}

func TestDecodeTaskFailure(t *testing.T) {
	e := envelope(EventTaskFailed, `{"task_id":"t-1","issue_id":"i-2","status":"failed","failure_reason":"boom","retry_pending":true}`)
	require.NotNil(t, e.Task, "task not decoded")
	assert.Equal(t, "boom", e.Task.FailureReason)
	assert.True(t, e.Task.RetryPending)
	assert.Equal(t, "t-1", e.Ref())
}

func TestDecodeToleratesMissingTypedPayload(t *testing.T) {
	assert.Nil(t, envelope(EventIssueStatusChanged, `{}`).Issue,
		"empty issue payload should decode to nil Issue")
}

func TestFilterAllowsConfiguredStatusesOnly(t *testing.T) {
	f := &Filter{IssueStatuses: map[string]bool{"in_review": true}, OnTaskFailed: true}

	assert.True(t, f.Allow(envelope(EventIssueStatusChanged, `{"issue":{"status":"in_review"}}`)))
	assert.False(t, f.Allow(envelope(EventIssueStatusChanged, `{"issue":{"status":"done"}}`)))
	assert.False(t, f.Allow(envelope(EventIssueStatusChanged, `{}`)), "missing snapshot must be filtered")
}

func TestFilterEmptyStatusListMeansAll(t *testing.T) {
	f := &Filter{}
	assert.True(t, f.Allow(envelope(EventIssueStatusChanged, `{"issue":{"status":"done"}}`)))
}

func TestFilterTaskSwitches(t *testing.T) {
	terminal := envelope(EventTaskFailed, `{"task_id":"t","retry_pending":false}`)
	retrying := envelope(EventTaskFailed, `{"task_id":"t","retry_pending":true}`)
	completed := envelope(EventTaskCompleted, `{"task_id":"t"}`)

	f := &Filter{OnTaskFailed: true, SkipRetryingTasks: true}
	assert.True(t, f.Allow(terminal), "terminal failure should pass")
	assert.False(t, f.Allow(retrying), "retrying failure should be skipped")

	f2 := &Filter{OnTaskFailed: true, SkipRetryingTasks: false}
	assert.True(t, f2.Allow(retrying), "retrying failure should pass when skipping disabled")

	f3 := &Filter{}
	assert.False(t, f3.Allow(terminal), "on_task_failed=false should filter failures")
	assert.False(t, f3.Allow(completed), "on_task_completed=false should filter completions")
}

func TestFilterHeartbeatAlwaysPasses(t *testing.T) {
	f := &Filter{} // everything off
	beat := envelope("", `{}`)
	beat.Trigger = TriggerSchedule
	assert.True(t, f.Allow(beat), "schedule heartbeat must always pass")
}

func TestDebouncerSendsLatestWithinWindow(t *testing.T) {
	d := NewDebouncer(30 * time.Millisecond)
	sent := make(chan string, 4)

	d.Submit("k", func() { sent <- "first" })
	d.Submit("k", func() { sent <- "second" })
	d.Submit("k", func() { sent <- "latest" })

	assert.Equal(t, "latest", <-sent)
	select {
	case extra := <-sent:
		t.Fatalf("extra emission %q", extra)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestDebouncerDistinctKeysIndependent(t *testing.T) {
	d := NewDebouncer(20 * time.Millisecond)
	sent := make(chan string, 4)

	d.Submit("a", func() { sent <- "a" })
	d.Submit("b", func() { sent <- "b" })

	for range 2 {
		select {
		case <-sent:
		case <-time.After(time.Second):
			t.Fatal("expected two emissions")
		}
	}
}

func TestDebouncerFlushSendsPending(t *testing.T) {
	d := NewDebouncer(time.Hour) // would never fire on its own
	sent := make(chan string, 1)
	d.Submit("k", func() { sent <- "flushed" })
	d.Flush()

	select {
	case got := <-sent:
		assert.Equal(t, "flushed", got)
	default:
		t.Fatal("flush did not emit pending")
	}
}

func TestJournalRoundTripAndRestart(t *testing.T) {
	path := t.TempDir() + "/nested/journal.jsonl"
	j, err := Open(path)
	require.NoError(t, err)

	assert.False(t, j.Seen("d1"), "fresh journal reports seen")
	require.NoError(t, j.Record("d1"))
	require.NoError(t, j.Record("d2"))
	j.Close()

	j2, err := Open(path)
	require.NoError(t, err)
	defer j2.Close()
	assert.True(t, j2.Seen("d1"), "record lost across reopen")
	assert.True(t, j2.Seen("d2"), "record lost across reopen")
	assert.False(t, j2.Seen("d3"), "phantom record")
}

func TestJournalRecordIsIdempotent(t *testing.T) {
	j, err := Open(t.TempDir() + "/journal.jsonl")
	require.NoError(t, err)
	defer j.Close()

	require.NoError(t, j.Record("d1"))
	assert.NoError(t, j.Record("d1"), "duplicate record errored")
}
