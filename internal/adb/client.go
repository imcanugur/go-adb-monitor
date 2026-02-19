package adb

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	// DefaultAddr is the default ADB server address.
	DefaultAddr = "127.0.0.1:5037"

	// defaultDialTimeout is the timeout for connecting to the ADB server.
	defaultDialTimeout = 5 * time.Second
)

// Client communicates with the ADB server over TCP.
type Client struct {
	addr string
}

// NewClient creates a new ADB client targeting the given server address.
// If addr is empty, DefaultAddr is used.
func NewClient(addr string) *Client {
	if addr == "" {
		addr = DefaultAddr
	}
	return &Client{addr: addr}
}

// Addr returns the ADB server address this client connects to.
func (c *Client) Addr() string {
	return c.addr
}

// dial opens a new TCP connection to the ADB server with the given context.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = defaultDialTimeout

	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServerNotRunning, err)
	}
	return conn, nil
}

// RawCommand opens a connection, sends the command, verifies OKAY, and returns
// the open connection for the caller to read the response stream.
// The caller is responsible for closing the returned connection.
func (c *Client) RawCommand(ctx context.Context, cmd string) (net.Conn, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}

	// Set deadline from context if present.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("setting deadline: %w", err)
		}
	}

	if err := writeCommand(conn, cmd); err != nil {
		conn.Close()
		return nil, fmt.Errorf("writing command %q: %w", cmd, err)
	}

	if err := readStatus(conn, cmd); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// Command sends a command and reads the full length-prefixed response.
func (c *Client) Command(ctx context.Context, cmd string) (string, error) {
	conn, err := c.RawCommand(ctx, cmd)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	return ReadLengthPrefixed(conn)
}

// DeviceCommand sends a command targeted at a specific device serial.
func (c *Client) DeviceCommand(ctx context.Context, serial, cmd string) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return "", fmt.Errorf("setting deadline: %w", err)
		}
	}

	// First, select the device transport.
	hostCmd := fmt.Sprintf("host:transport:%s", serial)
	if err := writeCommand(conn, hostCmd); err != nil {
		return "", fmt.Errorf("writing transport selection: %w", err)
	}
	if err := readStatus(conn, hostCmd); err != nil {
		return "", fmt.Errorf("selecting device %s: %w", serial, err)
	}

	// Then, send the actual command.
	if err := writeCommand(conn, cmd); err != nil {
		return "", fmt.Errorf("writing device command %q: %w", cmd, err)
	}
	if err := readStatus(conn, cmd); err != nil {
		return "", err
	}

	return readShellOutput(conn)
}

// Shell runs a shell command on the specified device and returns its output.
func (c *Client) Shell(ctx context.Context, serial, command string) (string, error) {
	shellCmd := fmt.Sprintf("shell:%s", command)
	return c.DeviceCommand(ctx, serial, shellCmd)
}

// ListDevices returns the current list of devices known to the ADB server.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	resp, err := c.Command(ctx, "host:devices-l")
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	return ParseDeviceList(resp), nil
}

// GetDeviceProp reads a system property from a device via getprop.
func (c *Client) GetDeviceProp(ctx context.Context, serial, prop string) (string, error) {
	out, err := c.Shell(ctx, serial, fmt.Sprintf("getprop %s", prop))
	if err != nil {
		return "", fmt.Errorf("getprop %s on %s: %w", prop, serial, err)
	}
	return strings.TrimSpace(out), nil
}

// TrackDevices opens a persistent connection using the track-devices-l command.
// The ADB server will push updated device lists whenever device state changes.
// The caller must read from the returned connection and close it when done.
func (c *Client) TrackDevices(ctx context.Context) (net.Conn, error) {
	conn, err := c.RawCommand(ctx, "host:track-devices-l")
	if err != nil {
		return nil, fmt.Errorf("track-devices: %w", err)
	}
	// Clear any deadline so the streaming connection stays open.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clearing deadline: %w", err)
	}
	return conn, nil
}

// ServerVersion returns the ADB server version.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	return c.Command(ctx, "host:version")
}
