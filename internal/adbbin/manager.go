package adbbin

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Manager handles discovery and lifecycle of the ADB binary.
// In production, it searches for a bundled ADB first, then falls back to PATH.
type Manager struct {
	log     *slog.Logger
	adbPath string
}

// New creates a new ADB binary manager.
// It searches for ADB in this order:
// 1. Bundled alongside the executable
// 2. ANDROID_HOME/platform-tools
// 3. System PATH
func New(log *slog.Logger) (*Manager, error) {
	m := &Manager{log: log.With("component", "adbbin")}

	path, err := m.findADB()
	if err != nil {
		return nil, err
	}

	m.adbPath = path
	m.log.Info("ADB binary found", "path", path)
	return m, nil
}

// Path returns the resolved ADB binary path.
func (m *Manager) Path() string {
	return m.adbPath
}

// EnsureServer starts the ADB server if it's not already running.
func (m *Manager) EnsureServer() error {
	m.log.Info("ensuring ADB server is running")

	cmd := exec.Command(m.adbPath, "start-server")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start ADB server: %w", err)
	}

	// Give the server a moment to initialize.
	time.Sleep(500 * time.Millisecond)
	m.log.Info("ADB server started")
	return nil
}

// KillServer stops the ADB server.
func (m *Manager) KillServer() error {
	cmd := exec.Command(m.adbPath, "kill-server")
	return cmd.Run()
}

// Version returns the ADB version string.
func (m *Manager) Version() (string, error) {
	cmd := exec.Command(m.adbPath, "version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("adb version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) findADB() (string, error) {
	candidates := m.buildCandidates()

	for _, path := range candidates {
		if isExecutable(path) {
			return path, nil
		}
	}

	// Last resort: check PATH.
	if path, err := exec.LookPath(adbBinaryName()); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("ADB binary not found. searched: %v and system PATH", candidates)
}

func (m *Manager) buildCandidates() []string {
	var candidates []string
	binName := adbBinaryName()

	// 1. Bundled alongside the executable.
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, binName),
			filepath.Join(exeDir, "adb", binName),
			filepath.Join(exeDir, "platform-tools", binName),
			filepath.Join(exeDir, "resources", "adb", binName),
		)
	}

	// 2. ANDROID_HOME / ANDROID_SDK_ROOT.
	for _, envVar := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if home := os.Getenv(envVar); home != "" {
			candidates = append(candidates,
				filepath.Join(home, "platform-tools", binName),
			)
		}
	}

	// 3. Common system locations.
	switch runtime.GOOS {
	case "linux":
		home, _ := os.UserHomeDir()
		candidates = append(candidates,
			filepath.Join(home, "Android", "Sdk", "platform-tools", binName),
			"/usr/bin/adb",
			"/usr/local/bin/adb",
		)
	case "darwin":
		home, _ := os.UserHomeDir()
		candidates = append(candidates,
			filepath.Join(home, "Library", "Android", "sdk", "platform-tools", binName),
			"/usr/local/bin/adb",
			"/opt/homebrew/bin/adb",
		)
	case "windows":
		home, _ := os.UserHomeDir()
		candidates = append(candidates,
			filepath.Join(home, "AppData", "Local", "Android", "Sdk", "platform-tools", binName),
			`C:\Program Files\Android\platform-tools\`+binName,
		)
	}

	return candidates
}

func adbBinaryName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // Windows doesn't have Unix permissions.
	}
	return info.Mode()&0111 != 0
}
