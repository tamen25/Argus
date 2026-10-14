// Package bounded reads untrusted streams with a size limit: HTTP response
// bodies from backends and model endpoints, and the output of subprocesses.
// A response or a process that sends more than expected must not be able to
// grow the engine's memory without limit.
package bounded

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Size limits for the streams Argus reads. They are far above what a working
// backend sends, and low enough that a misbehaving one cannot exhaust memory.
const (
	// BackendResponse caps a Mimir, Loki or Tempo response.
	BackendResponse = 32 << 20
	// ModelResponse caps one reply from a model endpoint (agent or judge).
	ModelResponse = 8 << 20
	// ProcessOutput caps what is kept of a subprocess's stdout.
	ProcessOutput = 8 << 20
	// ProcessErrors caps what is kept of a subprocess's stderr, which is only
	// quoted in an error message.
	ProcessErrors = 64 << 10
)

// ErrTooLarge is returned, wrapped, when a stream is longer than its limit.
var ErrTooLarge = errors.New("larger than the size limit")

// ReadAll reads r to the end, failing with ErrTooLarge once it passes limit
// bytes. One byte past the limit is read to tell "exactly at the limit" from
// "over it"; returning a silently cut prefix instead would hand the caller
// what looks like a complete response.
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response %w of %d bytes", ErrTooLarge, limit)
	}
	return b, nil
}

// DecodeJSON reads at most limit bytes of r and decodes them into v.
func DecodeJSON(r io.Reader, limit int64, v any) error {
	b, err := ReadAll(r, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Buffer is an io.Writer that keeps the first Limit bytes written to it and
// counts the rest. It is for a subprocess's output: Write never fails, so a
// chatty process is not killed by a broken pipe, it is only not believed
// past the limit. The zero value keeps nothing; set Limit.
type Buffer struct {
	Limit   int
	buf     []byte
	dropped int64
}

// Write keeps what fits under the limit and counts the rest as dropped.
func (b *Buffer) Write(p []byte) (int, error) {
	room := max(min(b.Limit-len(b.buf), len(p)), 0)
	b.buf = append(b.buf, p[:room]...)
	b.dropped += int64(len(p) - room)
	return len(p), nil
}

// Bytes returns what was kept.
func (b *Buffer) Bytes() []byte { return b.buf }

// Overflowed reports whether anything was dropped.
func (b *Buffer) Overflowed() bool { return b.dropped > 0 }

// String returns what was kept, followed by how much was dropped, if any.
func (b *Buffer) String() string {
	if b.dropped == 0 {
		return string(b.buf)
	}
	return fmt.Sprintf("%s… (%d more bytes not kept)", b.buf, b.dropped)
}
