// Package output re-exports the shared output contract from lib-agent-output,
// keeping the internal/output import path while the wire mechanism (format
// parsing, JSON encoding, error rendering) lives in one place; YAML encoding is
// supplied by the shared lib-agent-cli/yaml encoder. What stays local is
// agent-dlocal policy: the writer indirection used by tests, the NDJSON list
// writer, and the expose-aware redaction in redaction.go. (Migration shim.)
package output

import (
	"encoding/json"
	"io"
	"os"
	"sync"

	_ "github.com/shhac/lib-agent-cli/yaml" // registers the shared YAML encoder
	out "github.com/shhac/lib-agent-output"
)

var (
	writersMu sync.RWMutex
	stdout    io.Writer = os.Stdout
	stderr    io.Writer = os.Stderr
)

func Stdout() io.Writer {
	writersMu.RLock()
	defer writersMu.RUnlock()
	return stdout
}

func Stderr() io.Writer {
	writersMu.RLock()
	defer writersMu.RUnlock()
	return stderr
}

func SetWritersForTest(o, e io.Writer) func() {
	writersMu.Lock()
	previousOut := stdout
	previousErr := stderr
	if o != nil {
		stdout = o
	}
	if e != nil {
		stderr = e
	}
	writersMu.Unlock()
	return func() {
		writersMu.Lock()
		stdout = previousOut
		stderr = previousErr
		writersMu.Unlock()
	}
}

// Format and its values come from the shared contract; the NDJSON value is
// "jsonl" in both.
type Format = out.Format

const (
	FormatJSON   = out.FormatJSON
	FormatNDJSON = out.FormatNDJSON
)

// ResolveFormat keeps the family's one-arg, error-swallowing contract: an
// unparseable flag falls back to the default rather than surfacing.
func ResolveFormat(flagFormat string, defaultFormat Format) Format {
	f, err := out.ResolveFormat(flagFormat, defaultFormat)
	if err != nil {
		return defaultFormat
	}
	return f
}

// Print prunes nulls (opt-in) then encodes data in the given format via the
// shared encoder. Expose-aware redaction is applied by callers via Redact
// before Print.
func Print(data any, format Format, prune bool) {
	cleaned, ok := toCleanAny(data, prune)
	if !ok {
		return
	}
	_ = out.Print(Stdout(), cleaned, format, nil)
}

// Clean round-trips data through JSON and prunes nulls, so a caller can hand
// the result to an emitter that does not prune.
func Clean(data any) (any, bool) { return toCleanAny(data, true) }

func toCleanAny(data any, prune bool) (any, bool) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return nil, false
	}
	if prune {
		decoded = out.PruneNils(decoded)
	}
	return decoded, true
}

// NDJSONWriter writes one record per line.
type NDJSONWriter struct {
	enc *json.Encoder
}

func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &NDJSONWriter{enc: enc}
}

func (n *NDJSONWriter) WriteItem(item any) error {
	return n.enc.Encode(item)
}
