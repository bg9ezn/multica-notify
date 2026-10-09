// Command multica-notify is the event notification bridge for self-hosted
// Multica: it receives signed plugin-hook deliveries and fans the rendered
// notifications out to the configured channels (apprise, ntfy, webhook).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/hookserver"
	"github.com/bg9ezn/multica-notify/internal/message"
)

var version = "dev"

func main() {
	var (
		configPath  string
		showVersion bool
	)
	flag.StringVar(&configPath, "config", "",
		"path to config.yaml (defaults: /etc/multica-notify/config.yaml, ./config.yaml)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println("multica-notify", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if configPath == "" {
		for _, p := range []string{"/etc/multica-notify/config.yaml", "config.yaml"} {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
	}
	if configPath == "" {
		logger.Error("no config file found; pass -config")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	secret := os.Getenv(cfg.SigningSecretEnv)
	if secret == "" {
		logger.Error(fmt.Sprintf(
			"environment variable %s is not set — rotate the plugin token in Multica workspace settings to obtain the signing secret",
			cfg.SigningSecretEnv))
		os.Exit(1)
	}
	verifier, err := hookserver.NewVerifier(secret)
	if err != nil {
		logger.Error("invalid signing secret", "error", err)
		os.Exit(1)
	}

	registry := channel.NewRegistry()
	cur := buildDeps(cfg, registry, logger)
	handler := hookserver.NewHandler(verifier, cur)
	defer handler.Close(10 * time.Second)
	defer cur.Journal.Close()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("multica-notify listening",
			"addr", cfg.Listen, "tls", cfg.TLS != nil,
			"channels", len(cur.Channels), "debounce", time.Duration(cfg.Debounce.Window),
			"version", version)
		if cfg.TLS != nil {
			serveErr <- srv.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
		} else {
			serveErr <- srv.ListenAndServe()
		}
	}()

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("server exited", "error", err)
				os.Exit(1)
			}
			return
		case s := <-signals:
			if s == syscall.SIGHUP {
				reloaded, err := config.Load(configPath)
				if err != nil {
					logger.Error("config reload failed, keeping previous configuration", "error", err)
					continue
				}
				next := buildDeps(reloaded, registry, logger)
				handler.UpdateDeps(next)
				cur.Journal.Close()
				cur = next
				logger.Info("configuration reloaded",
					"channels", len(next.Channels), "debounce", time.Duration(reloaded.Debounce.Window))
				continue
			}
			logger.Info("shutting down", "signal", s.String())
			// Flush debounce windows first: a notification whose window was
			// about to close still goes out instead of being swallowed.
			if cur.Debouncer != nil {
				cur.Debouncer.Flush()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			return
		}
	}
}

// buildDeps materializes configuration into the handler's mutable state.
// Failures here are startup-fatal except the journal, which degrades.
func buildDeps(cfg *config.Config, registry *channel.Registry, logger *slog.Logger) *hookserver.Deps {
	renderer, err := message.NewRenderer(cfg.Templates)
	if err != nil {
		logger.Error("build templates", "error", err)
		os.Exit(1)
	}

	filter := &event.Filter{
		IssueStatuses:     statusSet(cfg.Filters.IssueStatuses),
		OnTaskCompleted:   cfg.Filters.OnTaskCompleted,
		OnTaskFailed:      *cfg.Filters.OnTaskFailed,
		SkipRetryingTasks: *cfg.Filters.SkipRetryingTasks,
	}

	var debouncer *event.Debouncer
	if w := time.Duration(cfg.Debounce.Window); w > 0 {
		debouncer = event.NewDebouncer(w)
	}

	journal, err := event.Open(cfg.IdempotencyJournal)
	if err != nil {
		logger.Warn("idempotency journal disabled", "error", err)
		journal = nil
	}

	var channels []channel.Channel
	for _, cc := range cfg.Channels {
		if !cc.IsEnabled() {
			logger.Info("channel disabled", "channel", cc.Name, "type", cc.Type, "reason", "enabled: false")
			continue
		}
		ch, err := registry.Build(cc.Type, cc.Name, cc.Options)
		if err != nil {
			logger.Error("build channel", "channel", cc.Name, "type", cc.Type, "error", err)
			os.Exit(1)
		}
		if ch == nil {
			logger.Info("channel disabled", "channel", cc.Name, "type", cc.Type, "reason", "adapter criteria (e.g. empty url)")
			continue
		}
		channels = append(channels, ch)
	}
	if len(channels) == 0 {
		logger.Error("no usable channels configured")
		os.Exit(1)
	}

	return &hookserver.Deps{
		Filter:    filter,
		Journal:   journal,
		Debouncer: debouncer,
		Renderer:  renderer,
		Channels:  channels,
		Logger:    logger,
	}
}

func statusSet(statuses []string) map[string]bool {
	if len(statuses) == 0 {
		return nil
	}
	m := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		m[s] = true
	}
	return m
}
