package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/message"
)

func newTestCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send a test notification through every enabled channel",
		Long: "Loads the configuration and sends one test message to each enabled\n" +
			"channel, reporting per-channel results and exit codes:\n" +
			"0 = every enabled channel sent, 1 = at least one failed.\n\n" +
			"This exercises the channel egress only - the hook server, journal and\n" +
			"signing secret are not involved. To test the full signed path from a\n" +
			"Multica-shaped delivery, use mocksender instead.",
		RunE: runTest(&configPath),
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "",
		"path to config.yaml (defaults: /etc/multica-notify/config.yaml, ./config.yaml)")
	return cmd
}

// outcome is the per-channel result of one test run.
type outcome struct {
	name, typ string
	enabled   bool
	built     bool
	err       error
	duration  time.Duration
}

func runTest(configPath *string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		resolveConfigPath(configPath)
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		registry := channel.NewRegistry()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		host, _ := os.Hostname()
		msg := message.Message{
			Title: "multica-notify test",
			Body: fmt.Sprintf("Test notification from %s at %s.\n"+
				"If you can read this, the channel configuration works.",
				host, time.Now().Format("2006-01-02 15:04:05 MST")),
			Meta: map[string]string{"event_type": "selftest"},
		}

		results := make([]outcome, 0, len(cfg.Channels))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, cc := range cfg.Channels {
			cc := cc
			res := outcome{name: cc.Name, typ: cc.Type}
			if !cc.IsEnabled() {
				res.enabled = false
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

		// Stable report order: as configured.
		sortResults(cfg, results)
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

		if len(cfg.Channels) == 0 {
			return fmt.Errorf("no channels configured")
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d enabled channels failed", failed, sent+failed)
		}
		if sent == 0 {
			return fmt.Errorf("no enabled channels to test")
		}
		return nil
	}
}

func sortResults(cfg *config.Config, results []outcome) {
	order := make(map[string]int, len(cfg.Channels))
	for i, cc := range cfg.Channels {
		order[cc.Name] = i
	}
	// Simple insertion sort - the slice is tiny and nearly ordered.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && order[results[j].name] < order[results[j-1].name]; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
