package event

// Filter decides which decoded events become notifications. It is pure and
// stateless; config survives reloads by swapping the *Filter atomically.
type Filter struct {
	// IssueStatuses: when non-empty, only issue.status_changed deliveries
	// whose new status is in the set pass. Empty means every status change.
	IssueStatuses map[string]bool

	// OnTaskCompleted toggles task.completed notifications.
	OnTaskCompleted bool

	// OnTaskFailed toggles task.failed notifications.
	OnTaskFailed bool

	// SkipRetryingTasks drops task.failed deliveries Multica marked as an
	// intermediate attempt (retry_pending=true) — the run may still recover.
	SkipRetryingTasks bool
}

// Allow reports whether the event should become a notification. Schedule
// deliveries (the heartbeat hook) always pass: their whole purpose is to be
// seen even when nothing else is flowing.
func (f *Filter) Allow(e *Event) bool {
	if e.Trigger == TriggerSchedule {
		return true
	}
	switch e.EventType {
	case EventIssueStatusChanged:
		if e.Issue == nil {
			return false
		}
		if len(f.IssueStatuses) == 0 {
			return true
		}
		return f.IssueStatuses[e.Issue.Status]
	case EventTaskCompleted:
		return f.OnTaskCompleted
	case EventTaskFailed:
		if !f.OnTaskFailed {
			return false
		}
		if f.SkipRetryingTasks && e.Task != nil && e.Task.RetryPending {
			return false
		}
		return true
	default:
		return false
	}
}

// SkipReason is the human-readable counterpart of a rejected Allow — the hook
// response includes it so a debugging operator sees why nothing fired.
func (f *Filter) SkipReason(e *Event) string {
	if e.Trigger == TriggerSchedule {
		return ""
	}
	switch e.EventType {
	case EventIssueStatusChanged:
		if e.Issue == nil {
			return "no issue snapshot in payload"
		}
		if len(f.IssueStatuses) > 0 && !f.IssueStatuses[e.Issue.Status] {
			return "status " + e.Issue.Status + " not in issue_statuses"
		}
	case EventTaskCompleted:
		if !f.OnTaskCompleted {
			return "on_task_completed disabled"
		}
	case EventTaskFailed:
		if !f.OnTaskFailed {
			return "on_task_failed disabled"
		}
		if f.SkipRetryingTasks && e.Task != nil && e.Task.RetryPending {
			return "retry_pending, skipping intermediate failure"
		}
	}
	return "event type not subscribed"
}
