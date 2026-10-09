// Command multica-notify is the event notification bridge for self-hosted
// Multica: it receives signed plugin-hook deliveries and fans the rendered
// notifications out to the configured channels (apprise, ntfy, webhook).
//
// Command structure (cobra):
//
//	multica-notify serve   [-c CONFIG] [-q|-v] [--log-file PATH]
//	multica-notify init-config <path> [--force]
//	multica-notify version
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/config"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/hookserver"
	"github.com/bg9ezn/multica-notify/internal/message"
	"github.com/bg9ezn/multica-notify/internal/version"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "multica-notify",
		Short: "Event notification bridge for self-hosted Multica",
		Long: "Receives signed Multica plugin-hook deliveries and fans the rendered\n" +
			"notifications out to the configured channels (apprise, ntfy, webhook).",
		Version:           version.Get(),
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.AddCommand(newServeCmd(), newInitConfigCmd(), newInitPluginCmd(), newTestCmd(), newVersionCmd())
	return root
}

// resolveConfigPath applies the default search locations when no --config
// was given. Shared by serve and test.
func resolveConfigPath(configPath *string) {
	if *configPath != "" {
		return
	}
	for _, p := range []string{"/etc/multica-notify/config.yaml", "config.yaml"} {
		if _, err := os.Stat(p); err == nil {
			*configPath = p
			return
		}
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Run: func(*cobra.Command, []string) {
			fmt.Println("multica-notify", version.Get())
		},
	}
}

func newInitConfigCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init-config <path>",
		Short: "Write the annotated example configuration to path",
		Long: "Writes the example configuration embedded in this binary — it is always\n" +
			"in lockstep with the supported fields. An existing file is kept unless\n" +
			"--force is given.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := config.InitConfig(args[0], force); err != nil {
				return err
			}
			fmt.Printf("wrote %s\nnext steps: edit the channels, set MULTICA_NOTIFY_SIGNING_SECRET, then run: multica-notify serve -c %s\n",
				args[0], args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

// slogLevel maps the -q/--quiet and -v/--verbose counters onto slog levels.
// Quiet wins over verbose: silencing must stay possible in one flag no
// matter how many -v were typed.
func slogLevel(quiet, verbose int) slog.Level {
	switch {
	case quiet > 0:
		return slog.LevelError
	case verbose > 0:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func newServeCmd() *cobra.Command {
	var (
		configPath     string
		logFile        string
		quiet, verbose int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the notification bridge (the long-running mode)",
		Long: "Starts the HTTPS hook server and fans accepted deliveries out to the\n" +
			"configured channels. Logs go to stderr; --log-file additionally mirrors\n" +
			"them into a file (disabled by default).",
		RunE: runServe(&configPath, &logFile, &quiet, &verbose),
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "",
		"path to config.yaml (defaults: /etc/multica-notify/config.yaml, ./config.yaml)")
	cmd.Flags().StringVar(&logFile, "log-file", "",
		"mirror logs into this file (append; default: stderr only)")
	cmd.Flags().CountVarP(&quiet, "quiet", "q", "suppress info/warn logs (repeatable)")
	cmd.Flags().CountVarP(&verbose, "verbose", "v", "verbose logging: debug level (repeatable)")
	return cmd
}

func runServe(configPath, logFile *string, quiet, verbose *int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		resolveConfigPath(configPath)
		if *configPath == "" {
			return fmt.Errorf("no config file found; pass --config")
		}

		logger := newLogger(*logFile, slogLevel(*quiet, *verbose))
		slog.SetDefault(logger)
		logger.Info("multica-notify starting", "version", version.Get())

		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		secret := os.Getenv(cfg.SigningSecretEnv)
		if secret == "" {
			return fmt.Errorf(
				"environment variable %s is not set — rotate the plugin token in Multica workspace settings to obtain the signing secret",
				cfg.SigningSecretEnv)
		}
		verifier, err := hookserver.NewVerifier(secret)
		if err != nil {
			return fmt.Errorf("invalid signing secret: %w", err)
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
				"muted", !cfg.IsEnabled(), "version", version.Get())
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
					return fmt.Errorf("server exited: %w", err)
				}
				return nil
			case s := <-signals:
				if s == syscall.SIGHUP {
					reloaded, err := config.Load(*configPath)
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
				return nil
			}
		}
	}
}

// newLogger builds the process logger. Output goes to stderr; when logFile
// is set the same stream is additionally mirrored (appended) into the file.
func newLogger(logFile string, level slog.Level) *slog.Logger {
	writers := []io.Writer{os.Stderr}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// Mirroring is best-effort: losing the file must not lose the process.
			fmt.Fprintf(os.Stderr, "multica-notify: log-file %s unavailable (%v); logging to stderr only\n", logFile, err)
		} else {
			writers = append(writers, f)
		}
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level}))
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
	// A muted bridge still builds its channels (config stays validated and
	// unmute needs no rebuild), so zero usable channels is only fatal when
	// notifications are actually expected to flow.
	if len(channels) == 0 && cfg.IsEnabled() {
		logger.Error("no usable channels configured")
		os.Exit(1)
	}

	muted := !cfg.IsEnabled()
	if muted {
		logger.Warn("notifications MUTED (enabled: false) — deliveries are accepted and journaled, fan-out suppressed")
	}

	return &hookserver.Deps{
		Muted:     muted,
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
