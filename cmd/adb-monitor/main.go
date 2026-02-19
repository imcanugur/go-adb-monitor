package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/event"
	"github.com/imcanugur/go-adb-monitor/internal/logging"
	"github.com/imcanugur/go-adb-monitor/internal/monitor"
	"github.com/imcanugur/go-adb-monitor/internal/tracker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// --- Flags ---
	var (
		adbAddr      = flag.String("adb-addr", adb.DefaultAddr, "ADB server address (host:port)")
		logLevel     = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		logFormat    = flag.String("log-format", "text", "Log format: text, json")
		propInterval = flag.Duration("prop-interval", monitor.DefaultPropInterval, "Device property collection interval")
		jsonOutput   = flag.Bool("json-events", false, "Print events as JSON to stdout")
	)
	flag.Parse()

	// --- Logger ---
	level := parseLogLevel(*logLevel)
	log := logging.New(logging.Config{
		Level:  level,
		Format: *logFormat,
	})

	log.Info("adb-monitor starting",
		"adb_addr", *adbAddr,
		"log_level", level.String(),
		"prop_interval", propInterval.String(),
	)

	// --- Context with signal handling ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- ADB Client ---
	client := adb.NewClient(*adbAddr)

	// Verify connectivity.
	version, err := client.ServerVersion(ctx)
	if err != nil {
		return fmt.Errorf("cannot connect to ADB server at %s: %w", *adbAddr, err)
	}
	log.Info("connected to ADB server", "version", version, "addr", *adbAddr)

	// --- Event Bus ---
	bus := event.NewBus(512)
	defer bus.Close()

	// Subscribe a logger/printer for all events.
	bus.Subscribe("stdout_printer", eventPrinter(log, *jsonOutput))

	// --- Device Tracker (streaming) ---
	deviceTracker := tracker.New(client, bus, log)

	// --- Device Monitor (per-device property collector) ---
	deviceMonitor := monitor.New(client, bus, log, monitor.Config{
		PropInterval: *propInterval,
	})

	// --- Run all components ---
	errCh := make(chan error, 2)

	go func() {
		errCh <- deviceTracker.Run(ctx)
	}()

	go func() {
		errCh <- deviceMonitor.Run(ctx)
	}()

	// Wait for context cancellation or first fatal error.
	select {
	case <-ctx.Done():
		log.Info("shutting down", "reason", "signal received")
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("component error: %w", err)
		}
	}

	return nil
}

// eventPrinter returns an event handler that logs each event.
func eventPrinter(log *slog.Logger, jsonOutput bool) event.Handler {
	return func(e event.Event) {
		if jsonOutput {
			data, err := json.Marshal(e)
			if err != nil {
				log.Error("failed to marshal event", "error", err)
				return
			}
			fmt.Fprintln(os.Stdout, string(data))
			return
		}

		switch e.Type {
		case event.DeviceConnected:
			log.Info("EVENT: device connected",
				"serial", e.Serial,
				"state", e.NewState,
				"model", e.Device.Model,
			)
		case event.DeviceDisconnected:
			log.Info("EVENT: device disconnected",
				"serial", e.Serial,
				"last_state", e.OldState,
			)
		case event.DeviceStateChanged:
			log.Info("EVENT: device state changed",
				"serial", e.Serial,
				"old", e.OldState,
				"new", e.NewState,
			)
		case event.DeviceProperties:
			log.Info("EVENT: device properties",
				"serial", e.Serial,
				"props", e.Props,
			)
		}
	}
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
