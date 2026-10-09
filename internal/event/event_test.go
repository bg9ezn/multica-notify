package event

import (
	"encoding/json"
	"testing"
	"time"
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
	if e.Issue == nil {
		t.Fatal("issue not decoded")
	}
	if e.Issue.Status != "in_review" || e.Issue.Number != 12 {
		t.Fatalf("issue decoded wrong: %+v", e.Issue)
	}
	if got := e.Ref(); got != "#12" {
		t.Fatalf("ref = %q, want #12", got)
	}
}

func TestDecodeTaskFailure(t *testing.T) {
	e := envelope(EventTaskFailed, `{"task_id":"t-1","issue_id":"i-2","status":"failed","failure_reason":"boom","retry_pending":true}`)
	if e.Task == nil || e.Task.FailureReason != "boom" || !e.Task.RetryPending {
		t.Fatalf("task decoded wrong: %+v", e.Task)
	}
	if got := e.Ref(); got != "t-1" {
		t.Fatalf("ref = %q, want t-1", got)
	}
}

func TestDecodeToleratesMissingTypedPayload(t *testing.T) {
	e := envelope(EventIssueStatusChanged, `{}`)
	if e.Issue != nil {
		t.Fatal("empty issue payload should decode to nil Issue")
	}
}

func TestFilterAllowsConfiguredStatusesOnly(t *testing.T) {
	f := &Filter{IssueStatuses: map[string]bool{"in_review": true}, OnTaskFailed: true}

	inReview := envelope(EventIssueStatusChanged, `{"issue":{"status":"in_review"}}`)
	done := envelope(EventIssueStatusChanged, `{"issue":{"status":"done"}}`)
	if !f.Allow(inReview) {
		t.Error("in_review should pass")
	}
	if f.Allow(done) {
		t.Error("done should be filtered")
	}
	if f.Allow(envelope(EventIssueStatusChanged, `{}`)) {
		t.Error("missing snapshot should be filtered")
	}
}

func TestFilterEmptyStatusListMeansAll(t *testing.T) {
	f := &Filter{}
	if !f.Allow(envelope(EventIssueStatusChanged, `{"issue":{"status":"done"}}`)) {
		t.Error("empty list should pass every status")
	}
}

func TestFilterTaskSwitches(t *testing.T) {
	terminal := envelope(EventTaskFailed, `{"task_id":"t","retry_pending":false}`)
	retrying := envelope(EventTaskFailed, `{"task_id":"t","retry_pending":true}`)

	f := &Filter{OnTaskFailed: true, SkipRetryingTasks: true}
	if !f.Allow(terminal) {
		t.Error("terminal failure should pass")
	}
	if f.Allow(retrying) {
		t.Error("retrying failure should be skipped")
	}

	f2 := &Filter{OnTaskFailed: true, SkipRetryingTasks: false}
	if !f2.Allow(retrying) {
		t.Error("retrying failure should pass when skipping disabled")
	}

	f3 := &Filter{}
	if f3.Allow(terminal) {
		t.Error("on_task_failed=false should filter failures")
	}
	if f3.Allow(envelope(EventTaskCompleted, `{"task_id":"t"}`)) {
		t.Error("on_task_completed=false should filter completions")
	}
}

func TestFilterHeartbeatAlwaysPasses(t *testing.T) {
	f := &Filter{} // everything off
	beat := envelope("", `{}`)
	beat.Trigger = TriggerSchedule
	if !f.Allow(beat) {
		t.Error("schedule heartbeat must always pass")
	}
}

func TestDebouncerSendsLatestWithinWindow(t *testing.T) {
	d := NewDebouncer(30 * time.Millisecond)
	sent := make(chan string, 4)

	d.Submit("k", func() { sent <- "first" })
	d.Submit("k", func() { sent <- "second" })
	d.Submit("k", func() { sent <- "latest" })

	got := <-sent
	if got != "latest" {
		t.Fatalf("emitted %q, want latest", got)
	}
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
		if got != "flushed" {
			t.Fatalf("got %q", got)
		}
	default:
		t.Fatal("flush did not emit pending")
	}
}

func TestJournalRoundTripAndRestart(t *testing.T) {
	path := t.TempDir() + "/nested/journal.jsonl"
	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if j.Seen("d1") {
		t.Fatal("fresh journal reports seen")
	}
	if err := j.Record("d1"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := j.Record("d2"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	j.Close()

	j2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer j2.Close()
	if !j2.Seen("d1") || !j2.Seen("d2") {
		t.Fatal("records lost across reopen")
	}
	if j2.Seen("d3") {
		t.Fatal("phantom record")
	}
}

func TestJournalRecordIsIdempotent(t *testing.T) {
	j, err := Open(t.TempDir() + "/journal.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	_ = j.Record("d1")
	if err := j.Record("d1"); err != nil {
		t.Fatalf("duplicate record errored: %v", err)
	}
}
