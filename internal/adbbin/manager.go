package adbbin

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Manager handles the ADB binary lifecycle.
// It can either use an embedded FS (extracted to a temp dir at startup)
// or discover ADB from the system.
type Manager struct {
	log     *slog.Logger
	adbPath string
	tempDir string // non-empty when we extracted embedded files
}

// New creates a Manager that searches the system for ADB.
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

// NewFromEmbed extracts the embedded platform-tools FS to a temp directory
// and returns a Manager that uses the extracted ADB binary.
// Call Cleanup() when done to remove the temp directory.
func NewFromEmbed(log *slog.Logger, embedded fs.FS) (*Manager, error) {
	m := &Manager{log: log.With("component", "adbbin")}

	tmpDir, err := os.MkdirTemp("", "adb-inspector-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	m.tempDir = tmpDir

	// Walk the embedded FS and extract all files.
	count := 0
	err = fs.WalkDir(embedded, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		target := filepath.Join(tmpDir, path)

		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		data, err := fs.ReadFile(embedded, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		if err := os.WriteFile(target, data, 0755); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		count++
		return nil
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("extract embedded ADB: %w", err)
	}

	binName := adbBinaryName()
	m.adbPath = filepath.Join(tmpDir, "platform-tools", binName)

	if !isExecutable(m.adbPath) {
		// Maybe files are at root level, not in platform-tools/ subdir.
		alt := filepath.Join(tmpDir, binName)
		if isExecutable(alt) {
			m.adbPath = alt
		} else {
			os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("embedded ADB binary not executable at %s or %s", m.adbPath, alt)
		}
	}

	m.log.Info("embedded ADB extracted", "path", m.adbPath, "files", count, "tempDir", tmpDir)
	return m, nil
}

// Path returns the resolved ADB binary path.
func (m *Manager) Path() string {
	return m.adbPath
}

// TempDir returns the temporary directory where ADB was extracted, or "" if not embedded.
func (m *Manager) TempDir() string {
	return m.tempDir
}

// Cleanup removes the temporary directory if one was created.
func (m *Manager) Cleanup() {
	if m.tempDir != "" {
		m.log.Info("cleaning up extracted ADB", "dir", m.tempDir)
		os.RemoveAll(m.tempDir)
		m.tempDir = ""
	}
}

// EnsureServer starts the ADB server if it's not already running.
func (m *Manager) EnsureServer() error {
	m.log.Info("ensuring ADB server is running")

	cmd := exec.Command(m.adbPath, "start-server")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set LD_LIBRARY_PATH for bundled shared libs.
	if m.tempDir != "" {
		libDir := filepath.Join(filepath.Dir(m.adbPath), "lib64")
		if _, err := os.Stat(libDir); err == nil {
			cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+libDir)
		}
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start ADB server: %w", err)
	}

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

	if path, err := exec.LookPath(adbBinaryName()); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("ADB binary not found. searched: %v and system PATH", candidates)
}

func (m *Manager) buildCandidates() []string {
	var candidates []string
	binName := adbBinaryName()

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, binName),
			filepath.Join(exeDir, "adb", binName),
			filepath.Join(exeDir, "platform-tools", binName),
			filepath.Join(exeDir, "resources", "adb", binName),
		)
	}

	for _, envVar := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if home := os.Getenv(envVar); home != "" {
			candidates = append(candidates,
				filepath.Join(home, "platform-tools", binName),
			)
		}
	}

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
		return true
	}
	return info.Mode()&0111 != 0
}
