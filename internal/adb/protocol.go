package adb

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	wireOkay = "OKAY"
	wireFail = "FAIL"
)

// writeCommand writes an ADB wire-protocol command to w.
// Format: 4-digit hex length prefix followed by the payload.
func writeCommand(w io.Writer, cmd string) error {
	msg := fmt.Sprintf("%04x%s", len(cmd), cmd)
	_, err := io.WriteString(w, msg)
	return err
}

// readStatus reads the 4-byte status response (OKAY or FAIL).
// On FAIL it reads the accompanying error message.
func readStatus(r io.Reader, cmd string) error {
	status := make([]byte, 4)
	if _, err := io.ReadFull(r, status); err != nil {
		return fmt.Errorf("reading status: %w", err)
	}

	switch string(status) {
	case wireOkay:
		return nil
	case wireFail:
		msg, err := ReadLengthPrefixed(r)
		if err != nil {
			return fmt.Errorf("reading fail message: %w", err)
		}
		return &ServerError{Command: cmd, Message: msg}
	default:
		return fmt.Errorf("%w: unexpected status %q", ErrProtocol, status)
	}
}

// ReadLengthPrefixed reads a 4-hex-digit length prefix and then that many bytes.
// Exported for use by the tracker package which reads from a raw ADB connection.
func ReadLengthPrefixed(r io.Reader) (string, error) {
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lengthBuf); err != nil {
		return "", fmt.Errorf("reading length prefix: %w", err)
	}

	var length uint32
	// Parse hex manually for performance.
	for _, b := range lengthBuf {
		length <<= 4
		switch {
		case b >= '0' && b <= '9':
			length |= uint32(b - '0')
		case b >= 'a' && b <= 'f':
			length |= uint32(b-'a') + 10
		case b >= 'A' && b <= 'F':
			length |= uint32(b-'A') + 10
		default:
			return "", fmt.Errorf("%w: invalid hex digit %q in length", ErrProtocol, b)
		}
	}

	if length == 0 {
		return "", nil
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", fmt.Errorf("reading payload (%d bytes): %w", length, err)
	}
	return string(payload), nil
}

// readShellOutput reads all remaining bytes from an ADB shell stream.
func readShellOutput(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("reading shell output: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// readBinaryLength reads a 4-byte little-endian length used in some ADB protocol
// extensions (unused in standard flow, kept for completeness).
func readBinaryLength(r io.Reader) (uint32, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return 0, err
	}
	return length, nil
}
