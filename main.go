package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/adbbin"
	"github.com/imcanugur/go-adb-monitor/internal/bridge"
	"github.com/imcanugur/go-adb-monitor/internal/logging"
	"github.com/imcanugur/go-adb-monitor/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	log := logging.New(logging.Config{
		Level:  slog.LevelInfo,
		Format: "text",
	})

	// Discover and start ADB server.
	adbMgr, err := adbbin.New(log)
	if err != nil {
		log.Warn("ADB binary not found, assuming server running externally", "error", err)
	} else {
		ver, _ := adbMgr.Version()
		log.Info("ADB binary resolved", "path", adbMgr.Path(), "version", ver)

		if err := adbMgr.EnsureServer(); err != nil {
			log.Error("failed to start ADB server", "error", err)
		}
	}

	// Build the application.
	app := bridge.NewApp(log, bridge.Config{
		ADBAddr:    adb.DefaultAddr,
		MaxWorkers: 100,
		StoreConfig: store.Config{
			MaxPackets:     50000,
			MaxConnections: 10000,
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.Startup(ctx)

	// Set up HTTP routes.
	mux := http.NewServeMux()
	app.RegisterRoutes(mux)

	// Serve frontend static files.
	mux.Handle("/", http.FileServer(http.Dir("frontend")))

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	go func() {
		log.Info("server starting", "addr", *addr, "url", "http://localhost"+*addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()

	srv.Shutdown(shutCtx)
	app.Shutdown()
}
