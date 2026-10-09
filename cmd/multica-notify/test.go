package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/message"
)

// outcome is the per-channel result of one test run.
type outcome struct {
	name, typ string
	enabled   bool
	built     bool
	err       error
	duration  time.Duration
}

var validNotifyTypes = map[string]bool{
	"info": true, "success": true, "warning": true, "error": true,
}

func validateNotifyType(t string) error {
	if !validNotifyTypes[t] {
		return fmt.Errorf("invalid notify type %q (valid: info, success, warning, error)", t)
	}
	return nil
}

// selectChannels narrows the configured channels to the wanted names,
// preserving configuration order (report order matches the config file). An
// empty wanted list selects everything; unknown names are reported with the
// configured names so typos are obvious.
func selectChannels(all []config.ChannelConfig, wanted []string) ([]config.ChannelConfig, error) {
	if len(wanted) == 0 {
		return all, nil
	}
	wantedSet := make(map[string]bool, len(wanted))
	for _, w := range wanted {
		wantedSet[w] = true
	}
	byName := make(map[string]int, len(all))
	for i, c := range all {
		byName[c.Name] = i
	}
	out := make([]config.ChannelConfig, 0, len(wanted))
	for _, c := range all {
		if wantedSet[c.Name] {
			out = append(out, c)
		}
	}
	if len(out) != len(wanted) {
		var missing, names []string
		for _, w := range wanted {
			if _, ok := byName[w]; !ok {
				missing = append(missing, `"`+w+`"`)
			}
		}
		for _, c := range all {
			names = append(names, c.Name)
		}
		return nil, fmt.Errorf("unknown channel(s) %s (configured: %s)",
			strings.Join(missing, ", "), strings.Join(names, ", "))
	}
	return out, nil
}

func newTestCmd() *cobra.Command {
	var (
		configPath             string
		onlyChannels           []string
		title, body            string
		notifyType             string
		eventType, eventStatus string
		retryPending           bool
		ignoreFilters          bool
	)
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send a test notification through every enabled channel",
		Long: "Loads the configuration and sends one test message to the enabled\n" +
			"channels, reporting per-channel results and exit codes:\n" +
			"0 = every tested channel sent, 1 = at least one failed.\n\n" +
			"Two modes:\n" +
			"  channel test (default) - one fixed message per enabled channel; the\n" +
			"    filter pipeline is bypassed. Channel egress only.\n" +
			"  event simulation (--event) - synthesizes a Multica-shaped delivery\n" +
			"    and runs it through the configured filter pipeline: allowed events\n" +
			"    are rendered and delivered, filtered ones are reported and skipped\n" +
			"    (exit 0 - a rejection is the filter working as configured). Use\n" +
			"    --ignore-filters to deliver regardless.\n\n" +
			"To test the full signed path from a Multica-shaped delivery, use\n" +
			"mocksender instead.",
		RunE: runTest(&configPath, &onlyChannels, &title, &body, &notifyType,
			&eventType, &eventStatus, &retryPending, &ignoreFilters),
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "",
		"path to config.yaml (defaults: /etc/multica-notify/config.yaml, ./config.yaml)")
	cmd.Flags().StringSliceVar(&onlyChannels, "channel", nil,
		"test only these channels by name (repeatable; default: all enabled channels)")
	cmd.Flags().StringVar(&title, "title", "", "override the test message title")
	cmd.Flags().StringVar(&body, "message", "", "override the test message body")
	cmd.Flags().StringVar(&notifyType, "type", "info",
		"notify type: info, success, warning or error")
	cmd.Flags().StringVar(&eventType, "event", "",
		"simulate a Multica event through the filter pipeline: issue.status_changed, task.completed or task.failed")
	cmd.Flags().StringVar(&eventStatus, "status", "in_review",
		"issue status for --event issue.status_changed (e.g. in_review, done)")
	cmd.Flags().BoolVar(&retryPending, "retry-pending", false,
		"mark the simulated task.failed as retry_pending (exercises skip_retrying_tasks)")
	cmd.Flags().BoolVar(&ignoreFilters, "ignore-filters", false,
		"deliver even when the configured filters would reject the event")
	return cmd
}

func runTest(configPath *string, onlyChannels *[]string, title, body, notifyType *string,
	eventType, eventStatus *string, retryPending, ignoreFilters *bool,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		if err := validateNotifyType(*notifyType); err != nil {
			return err
		}
		resolveConfigPath(configPath)
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		selected, err := selectChannels(cfg.Channels, *onlyChannels)
		if err != nil {
			return err
		}

		registry := channel.NewRegistry()
		renderer, err := message.NewRenderer(cfg.Templates)
		if err != nil {
			return fmt.Errorf("build templates: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		simulating := *eventType != ""
		var e *event.Event
		var msg message.Message
		if simulating {
			// Event simulation: the same pipeline as real deliveries - decode,
			// filter, render. A rejection is a SUCCESSFUL test of the filter
			// (exit 0); only channel send failures fail the run.
			e, err = simulateEvent(*eventType, *eventStatus, *retryPending)
			if err != nil {
				return err
			}
			cfgFilter := filterFromConfig(cfg)
			if !cfgFilter.Allow(e) {
				reason := cfgFilter.SkipReason(e)
				fmt.Printf("FILTERED: %s\n", reason)
				if !*ignoreFilters {
					fmt.Println("the filter rejected this event as configured - nothing was delivered")
					fmt.Println("(use --ignore-filters to deliver it anyway)")
					return nil
				}
				fmt.Println("--ignore-filters: delivering anyway")
			}
			msg, err = renderer.Render(e)
			if err != nil {
				return fmt.Errorf("render notification: %w", err)
			}
		} else {
			host, _ := os.Hostname()
			msg = message.Message{
				Title: "multica-notify test",
				Body: fmt.Sprintf("Test notification from %s at %s.\n"+
					"If you can read this, the channel configuration works.",
					host, time.Now().Format("2006-01-02 15:04:05 MST")),
				Meta: map[string]string{"event_type": "selftest"},
			}
		}
		// Explicit overrides win over the rendered defaults in both modes.
		if *title != "" {
			msg.Title = *title
		}
		if *body != "" {
			msg.Body = *body
		}
		if *notifyType != "" {
			msg.Type = *notifyType
		}

		results := make([]outcome, 0, len(selected))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, cc := range selected {
			cc := cc
			res := outcome{name: cc.Name, typ: cc.Type}
			if !cc.IsEnabled() {
				results = append(results, res)
				continue
			}
			res.enabled = true
			ch, err := registry.Build(cc.Type, cc.Name, cc.Options)
			if err != nil {
				res.err = fmt.Errorf("build: %w", err)
				results = append(results, res)
				continue
			}
			if ch == nil {
				res.err = fmt.Errorf("adapter criteria not met (e.g. empty url)")
				results = append(results, res)
				continue
			}
			res.built = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				res.err = ch.Send(ctx, msg)
				res.duration = time.Since(start)
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
			}()
		}
		wg.Wait()

		sortResults(selected, results)
		failed, sent := 0, 0
		for _, res := range results {
			switch {
			case !res.enabled:
				fmt.Printf("SKIP  %-12s (%s): disabled\n", res.name, res.typ)
			case !res.built:
				failed++
				fmt.Printf("FAIL  %-12s (%s): %v\n", res.name, res.typ, res.err)
			case res.err != nil:
				failed++
				fmt.Printf("FAIL  %-12s (%s) after %s: %v\n", res.name, res.typ, res.duration.Round(time.Millisecond), res.err)
			default:
				sent++
				fmt.Printf("OK    %-12s (%s) in %s\n", res.name, res.typ, res.duration.Round(time.Millisecond))
			}
		}
		fmt.Printf("%d sent, %d failed, %d skipped\n", sent, failed, len(results)-sent-failed)

		if len(selected) == 0 {
			return fmt.Errorf("no channels configured")
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d tested channels failed", failed, sent+failed)
		}
		if sent == 0 {
			return fmt.Errorf("no enabled channels to test")
		}
		return nil
	}
}

// simulateEvent builds a Multica-shaped event for the requested type. The
// synthetic ids are constants on purpose: they mark the traffic as selftest
// in receiver-side logs and keep repeated runs comparable.
func simulateEvent(eventType, status string, retryPending bool) (*event.Event, error) {
	const (
		selfIssueID = "00000000-0000-0000-0000-000000000001"
		selfTaskID  = "selftest-task"
	)
	var input map[string]any
	switch eventType {
	case event.EventIssueStatusChanged:
		input = map[string]any{
			"issue": map[string]any{
				"id": selfIssueID, "number": 0,
				"title": "multica-notify selftest", "status": status,
			},
			"status_changed": true,
		}
	case event.EventTaskCompleted, event.EventTaskFailed:
		input = map[string]any{
			"task_id": selfTaskID, "issue_id": selfIssueID,
			"status":         eventType,
			"failure_reason": "selftest failure reason",
			"retry_pending":  retryPending,
		}
	default:
		return nil, fmt.Errorf(
			"unsupported event %q (valid: issue.status_changed, task.completed, task.failed)", eventType)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hookKey := "issue_status"
	if strings.HasPrefix(eventType, "task.") {
		hookKey = "task_lifecycle"
	}
	return event.Decode(event.Envelope{
		HookKey:      hookKey,
		Trigger:      event.TriggerEvent,
		EventType:    eventType,
		DeliveryID:   "selftest",
		WorkspaceID:  "selftest",
		Installation: "selftest",
		IssueID:      selfIssueID,
		OccurredAt:   time.Now().UTC(),
		Input:        raw,
	}), nil
}

// filterFromConfig materializes the event filter from configuration (the
// same rules the serve pipeline applies).
func filterFromConfig(cfg *config.Config) *event.Filter {
	return &event.Filter{
		IssueStatuses:     statusSet(cfg.Filters.IssueStatuses),
		OnTaskCompleted:   cfg.Filters.OnTaskCompleted,
		OnTaskFailed:      *cfg.Filters.OnTaskFailed,
		SkipRetryingTasks: *cfg.Filters.SkipRetryingTasks,
	}
}

func sortResults(cfgChannels []config.ChannelConfig, results []outcome) {
	order := make(map[string]int, len(cfgChannels))
	for i, cc := range cfgChannels {
		order[cc.Name] = i
	}
	// Insertion sort - the slice is tiny and nearly ordered.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && order[results[j].name] < order[results[j-1].name]; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
