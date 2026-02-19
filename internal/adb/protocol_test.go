package adb

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestWriteCommand(t *testing.T) {
	var buf bytes.Buffer
	err := writeCommand(&buf, "host:version")
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "000chost:version"
	if got != want {
		t.Errorf("writeCommand: got %q, want %q", got, want)
	}
}

func TestWriteCommand_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := writeCommand(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "0000"
	if got != want {
		t.Errorf("writeCommand empty: got %q, want %q", got, want)
	}
}

func TestReadLengthPrefixed(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"simple", "000chello, world", "hello, world", false},
		{"empty payload", "0000", "", false},
		{"hex digits", "000a0123456789", "0123456789", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, err := ReadLengthPrefixed(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadLengthPrefixed_InvalidHex(t *testing.T) {
	r := strings.NewReader("zzzzhello")
	_, err := ReadLengthPrefixed(r)
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestReadLengthPrefixed_ShortRead(t *testing.T) {
	// Claim 10 bytes but only provide 5.
	r := strings.NewReader("000ahello")
	_, err := ReadLengthPrefixed(r)
	if err == nil {
		t.Fatal("expected error for short read")
	}
}

func TestReadStatus_OKAY(t *testing.T) {
	r := strings.NewReader("OKAY")
	err := readStatus(r, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadStatus_FAIL(t *testing.T) {
	msg := "device not found"
	fail := fmt.Sprintf("FAIL%04x%s", len(msg), msg)
	r := strings.NewReader(fail)
	err := readStatus(r, "host:transport:xyz")
	if err == nil {
		t.Fatal("expected error for FAIL response")
	}
	var serverErr *ServerError
	if !isServerError(err, &serverErr) {
		t.Fatalf("expected ServerError, got %T: %v", err, err)
	}
	if serverErr.Message != msg {
		t.Errorf("message: got %q, want %q", serverErr.Message, msg)
	}
}

func TestReadStatus_InvalidStatus(t *testing.T) {
	r := strings.NewReader("BAAD")
	err := readStatus(r, "test")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestReadStatus_EOF(t *testing.T) {
	r := strings.NewReader("")
	err := readStatus(r, "test")
	if err == nil {
		t.Fatal("expected error for EOF")
	}
}

func TestReadShellOutput(t *testing.T) {
	input := "  some output with whitespace  \n\n"
	got, err := readShellOutput(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got != "some output with whitespace" {
		t.Errorf("got %q", got)
	}
}

func TestReadShellOutput_Empty(t *testing.T) {
	got, err := readShellOutput(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// helper for errors.As without importing errors in test
func isServerError(err error, target **ServerError) bool {
	for err != nil {
		if se, ok := err.(*ServerError); ok {
			*target = se
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// failReader always returns an error.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReadLengthPrefixed_ReadError(t *testing.T) {
	_, err := ReadLengthPrefixed(failReader{})
	if err == nil {
		t.Fatal("expected error")
	}
}
