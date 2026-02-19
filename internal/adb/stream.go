package adb

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// ShellStream opens a persistent, streaming shell connection to a device.
// The returned ReadCloser continuously delivers stdout from the command.
// The caller MUST close it when done. Closing also cancels the underlying connection.
type ShellStream struct {
	conn   net.Conn
	cancel context.CancelFunc
}

// Read implements io.Reader; reads raw shell output bytes.
func (s *ShellStream) Read(p []byte) (int, error) {
	return s.conn.Read(p)
}

// Close terminates the streaming shell session.
func (s *ShellStream) Close() error {
	s.cancel()
	return s.conn.Close()
}

// OpenShellStream opens a long-lived shell command on the device identified by serial.
// The returned ShellStream delivers continuous output (e.g. from tcpdump).
// A background goroutine watches ctx for cancellation and closes the connection.
func (c *Client) OpenShellStream(ctx context.Context, serial, command string) (*ShellStream, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("dialing for shell stream: %w", err)
	}

	// Clear any dial deadline; this is a long-lived connection.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clearing deadline: %w", err)
	}

	// Select device transport.
	hostCmd := fmt.Sprintf("host:transport:%s", serial)
	if err := writeCommand(conn, hostCmd); err != nil {
		conn.Close()
		return nil, fmt.Errorf("writing transport: %w", err)
	}
	if err := readStatus(conn, hostCmd); err != nil {
		conn.Close()
		return nil, fmt.Errorf("selecting device %s: %w", serial, err)
	}

	// Open shell.
	shellCmd := fmt.Sprintf("shell:%s", command)
	if err := writeCommand(conn, shellCmd); err != nil {
		conn.Close()
		return nil, fmt.Errorf("writing shell command: %w", err)
	}
	if err := readStatus(conn, shellCmd); err != nil {
		conn.Close()
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)

	stream := &ShellStream{
		conn:   conn,
		cancel: cancel,
	}

	// Close the connection when the parent or stream context is cancelled.
	go func() {
		<-streamCtx.Done()
		conn.Close()
	}()

	return stream, nil
}

// ExecOutput runs a shell command on a device and streams all output via the returned Reader.
// This is a convenience wrapper for short-lived commands where you want streaming reads.
func (c *Client) ExecOutput(ctx context.Context, serial, command string) (io.ReadCloser, error) {
	return c.OpenShellStream(ctx, serial, command)
}
