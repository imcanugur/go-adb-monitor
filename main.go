package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
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

// Embed the frontend assets and platform-tools (ADB) into the binary.
// This makes the output a completely self-contained single file.
//
//go:embed frontend
var frontendFS embed.FS

//go:embed platform-tools
var platformToolsFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	log := logging.New(logging.Config{
		Level:  slog.LevelInfo,
		Format: "text",
	})

	// Extract embedded ADB to a temp dir and start the server.
	adbMgr, err := adbbin.NewFromEmbed(log, platformToolsFS)
	if err != nil {
		log.Warn("embedded ADB extraction failed, trying system ADB", "error", err)
		// Fallback: try to find ADB on the system.
		adbMgr, err = adbbin.New(log)
		if err != nil {
			log.Error("ADB not available — network capture will not work", "error", err)
		}
	}

	if adbMgr != nil {
		defer adbMgr.Cleanup()

		ver, _ := adbMgr.Version()
		log.Info("ADB ready", "path", adbMgr.Path(), "version", ver)

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

	// Serve embedded frontend files.
	frontendSub, _ := fs.Sub(frontendFS, "frontend")
	mux.Handle("/", http.FileServer(http.FS(frontendSub)))

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
