package bridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/capture"
	"github.com/imcanugur/go-adb-monitor/internal/event"
	"github.com/imcanugur/go-adb-monitor/internal/pool"
	"github.com/imcanugur/go-adb-monitor/internal/store"
	"github.com/imcanugur/go-adb-monitor/internal/tracker"
)

// App is the main application controller.
// It wires ADB tracking, network capture, and exposes HTTP API + SSE events.
type App struct {
	ctx    context.Context
	cancel context.CancelFunc
	log    *slog.Logger

	client  *adb.Client
	bus     *event.Bus
	tracker *tracker.Tracker
	store   *store.Store
	pool    *pool.Pool
	sse     *SSEHub

	mu       sync.Mutex
	captures map[string]*deviceCapture // serial -> active capture
	devices  map[string]adb.Device     // serial -> device
}

// deviceCapture tracks per-device capture state.
type deviceCapture struct {
	engine *capture.Engine
	cancel context.CancelFunc
}

// Config holds application configuration.
type Config struct {
	ADBAddr     string
	MaxWorkers  int
	StoreConfig store.Config
}

// NewApp creates the application controller.
func NewApp(log *slog.Logger, cfg Config) *App {
	if cfg.ADBAddr == "" {
		cfg.ADBAddr = adb.DefaultAddr
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 100
	}

	client := adb.NewClient(cfg.ADBAddr)
	bus := event.NewBus(1024)
	dataStore := store.New(cfg.StoreConfig)
	workerPool := pool.New(cfg.MaxWorkers, log)
	deviceTracker := tracker.New(client, bus, log)

	return &App{
		log:      log.With("component", "bridge"),
		client:   client,
		bus:      bus,
		tracker:  deviceTracker,
		store:    dataStore,
		pool:     workerPool,
		sse:      NewSSEHub(),
		captures: make(map[string]*deviceCapture),
		devices:  make(map[string]adb.Device),
	}
}

// Startup initializes the application: starts the device tracker, subscribes to events.
func (a *App) Startup(ctx context.Context) {
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.log.Info("application starting")

	// Subscribe to device events for internal tracking + SSE emission.
	a.bus.Subscribe("bridge_devices", a.handleDeviceEvent)

	// Start the device tracker.
	go func() {
		if err := a.tracker.Run(a.ctx); err != nil && a.ctx.Err() == nil {
			a.log.Error("tracker failed", "error", err)
		}
	}()

	// Notify UI on store changes.
	a.store.SetOnChange(func() {
		a.sse.Broadcast("store:updated", map[string]interface{}{})
	})
}

// Shutdown gracefully stops all captures and background work.
func (a *App) Shutdown() {
	a.log.Info("application shutting down")
	a.stopAllCaptures()
	a.bus.Close()
	if a.cancel != nil {
		a.cancel()
	}
	a.pool.Wait()
}

// RegisterRoutes mounts all HTTP API routes on the given mux.
func (a *App) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices", a.handleGetDevices)
	mux.HandleFunc("POST /api/devices/refresh", a.handleRefreshDevices)
	mux.HandleFunc("GET /api/adb/version", a.handleGetADBVersion)
	mux.HandleFunc("POST /api/capture/start-all", a.handleStartAllCaptures)
	mux.HandleFunc("POST /api/capture/stop-all", a.handleStopAllCaptures)
	mux.HandleFunc("POST /api/capture/start/{serial}", a.handleStartCapture)
	mux.HandleFunc("POST /api/capture/stop/{serial}", a.handleStopCapture)
	mux.HandleFunc("GET /api/capture/status", a.handleGetCaptureStatus)
	mux.HandleFunc("GET /api/packets/{serial}", a.handleGetDevicePackets)
	mux.HandleFunc("GET /api/packets", a.handleGetRecentPackets)
	mux.HandleFunc("GET /api/connections/{serial}", a.handleGetDeviceConnections)
	mux.HandleFunc("GET /api/connections", a.handleGetRecentConnections)
	mux.HandleFunc("GET /api/store/stats", a.handleGetStoreStats)
	mux.HandleFunc("GET /api/pool/stats", a.handleGetPoolStats)
	mux.HandleFunc("POST /api/clear", a.handleClearData)
	mux.Handle("GET /api/events", a.sse)
}

// ============================================
// Device event handler (internal)
// ============================================

func (a *App) handleDeviceEvent(e event.Event) {
	switch e.Type {
	case event.DeviceConnected:
		if e.Device != nil {
			a.mu.Lock()
			a.devices[e.Serial] = *e.Device
			a.mu.Unlock()
		}
		a.sse.Broadcast("device:connected", e)

	case event.DeviceDisconnected:
		a.mu.Lock()
		delete(a.devices, e.Serial)
		a.mu.Unlock()
		a.StopCapture(e.Serial)
		a.sse.Broadcast("device:disconnected", e)

	case event.DeviceStateChanged:
		if e.Device != nil {
			a.mu.Lock()
			a.devices[e.Serial] = *e.Device
			a.mu.Unlock()
		}
		a.sse.Broadcast("device:state_changed", e)
	}
}

// ============================================
// Business logic methods
// ============================================

// GetDevices returns all currently known devices.
func (a *App) GetDevices() []adb.Device {
	a.mu.Lock()
	defer a.mu.Unlock()

	devices := make([]adb.Device, 0, len(a.devices))
	for _, d := range a.devices {
		devices = append(devices, d)
	}
	return devices
}

// RefreshDevices forces a re-read of the device list from ADB.
func (a *App) RefreshDevices() ([]adb.Device, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	devices, err := a.client.ListDevices(ctx)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.devices = make(map[string]adb.Device, len(devices))
	for _, d := range devices {
		a.devices[d.Serial] = d
	}
	a.mu.Unlock()

	a.sse.Broadcast("devices:refreshed", devices)
	return devices, nil
}

// StartCapture begins network capture on the specified device.
func (a *App) StartCapture(serial string) error {
	a.mu.Lock()
	if _, running := a.captures[serial]; running {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	engine := capture.NewEngine(a.client, a.log, serial, capture.ModeAuto)
	captureCtx, captureCancel := context.WithCancel(a.ctx)

	a.mu.Lock()
	a.captures[serial] = &deviceCapture{
		engine: engine,
		cancel: captureCancel,
	}
	a.mu.Unlock()

	return a.pool.Submit(a.ctx, pool.Task{
		Name: "capture:" + serial,
		Fn: func(ctx context.Context) error {
			go a.drainPackets(serial, engine.Packets(), captureCtx.Done())
			go a.drainConnections(serial, engine.Connections(), captureCtx.Done())

			err := engine.Run(captureCtx)

			a.mu.Lock()
			delete(a.captures, serial)
			a.mu.Unlock()

			a.sse.Broadcast("capture:stopped", map[string]string{
				"serial": serial,
			})
			return err
		},
	})
}

// StopCapture stops network capture on the specified device.
func (a *App) StopCapture(serial string) {
	a.mu.Lock()
	dc, ok := a.captures[serial]
	if ok {
		dc.cancel()
		delete(a.captures, serial)
	}
	a.mu.Unlock()

	if ok {
		a.log.Info("capture stopped", "serial", serial)
	}
}

// StartAllCaptures begins capture on all connected online devices.
func (a *App) StartAllCaptures() int {
	a.mu.Lock()
	var serials []string
	for serial, dev := range a.devices {
		if dev.State.IsOnline() {
			serials = append(serials, serial)
		}
	}
	a.mu.Unlock()

	started := 0
	for _, serial := range serials {
		if err := a.StartCapture(serial); err == nil {
			started++
		}
	}
	return started
}

// StopAllCaptures stops capture on all devices.
func (a *App) StopAllCaptures() {
	a.stopAllCaptures()
}

// GetCaptureStatus returns which devices have active captures.
func (a *App) GetCaptureStatus() map[string]capture.CaptureStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make(map[string]capture.CaptureStats, len(a.captures))
	for serial, dc := range a.captures {
		result[serial] = dc.engine.Stats()
	}
	return result
}

// GetADBVersion returns the ADB server version string.
func (a *App) GetADBVersion() (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return a.client.ServerVersion(ctx)
}

// ============================================
// HTTP Handlers
// ============================================

func (a *App) handleGetDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.GetDevices())
}

func (a *App) handleRefreshDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := a.RefreshDevices()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (a *App) handleGetADBVersion(w http.ResponseWriter, r *http.Request) {
	version, err := a.GetADBVersion()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": version})
}

func (a *App) handleStartCapture(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}
	if err := a.StartCapture(serial); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "serial": serial})
}

func (a *App) handleStopCapture(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}
	a.StopCapture(serial)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "serial": serial})
}

func (a *App) handleStartAllCaptures(w http.ResponseWriter, r *http.Request) {
	count := a.StartAllCaptures()
	writeJSON(w, http.StatusOK, map[string]int{"started": count})
}

func (a *App) handleStopAllCaptures(w http.ResponseWriter, r *http.Request) {
	a.StopAllCaptures()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (a *App) handleGetCaptureStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.GetCaptureStatus())
}

func (a *App) handleGetRecentPackets(w http.ResponseWriter, r *http.Request) {
	n := queryInt(r, "n", 200)
	writeJSON(w, http.StatusOK, a.store.GetRecentPackets(n))
}

func (a *App) handleGetDevicePackets(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	n := queryInt(r, "n", 200)
	writeJSON(w, http.StatusOK, a.store.GetPacketsBySerial(serial, n))
}

func (a *App) handleGetRecentConnections(w http.ResponseWriter, r *http.Request) {
	n := queryInt(r, "n", 200)
	writeJSON(w, http.StatusOK, a.store.GetRecentConnections(n))
}

func (a *App) handleGetDeviceConnections(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	n := queryInt(r, "n", 200)
	writeJSON(w, http.StatusOK, a.store.GetConnectionsBySerial(serial, n))
}

func (a *App) handleGetStoreStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.store.Stats())
}

func (a *App) handleGetPoolStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{
		"active":      a.pool.ActiveCount(),
		"max_workers": a.pool.MaxWorkers(),
	})
}

func (a *App) handleClearData(w http.ResponseWriter, r *http.Request) {
	a.store.Clear()
	a.sse.Broadcast("store:cleared", map[string]interface{}{})
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// ============================================
// Internal helpers
// ============================================

func (a *App) drainPackets(serial string, ch <-chan capture.NetworkPacket, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}
			a.store.AddPacket(pkt)
			a.sse.Broadcast("packet:new", pkt)
		}
	}
}

func (a *App) drainConnections(serial string, ch <-chan capture.Connection, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case conn, ok := <-ch:
			if !ok {
				return
			}
			a.store.AddConnection(conn)
			a.sse.Broadcast("connection:new", conn)
		}
	}
}

func (a *App) stopAllCaptures() {
	a.mu.Lock()
	for serial, dc := range a.captures {
		dc.cancel()
		a.log.Debug("stopped capture", "serial", serial)
	}
	a.captures = make(map[string]*deviceCapture)
	a.mu.Unlock()
}

// ============================================
// JSON utilities
// ============================================

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data == nil {
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func queryInt(r *http.Request, key string, def int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
