package adb

import (
	"errors"
	"fmt"
)

var (
	// ErrServerNotRunning indicates the ADB server is not reachable.
	ErrServerNotRunning = errors.New("adb server not running or not reachable")

	// ErrProtocol indicates a wire-protocol violation.
	ErrProtocol = errors.New("adb protocol error")

	// ErrDeviceNotFound indicates the target device is not connected.
	ErrDeviceNotFound = errors.New("device not found")

	// ErrCommandFailed indicates the ADB server rejected a command.
	ErrCommandFailed = errors.New("adb command failed")

	// ErrConnectionClosed indicates the connection was closed unexpectedly.
	ErrConnectionClosed = errors.New("connection closed")
)

// ServerError wraps an error returned by the ADB server with the server's message.
type ServerError struct {
	Command string
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("adb server error for command %q: %s", e.Command, e.Message)
}

func (e *ServerError) Unwrap() error {
	return ErrCommandFailed
}
