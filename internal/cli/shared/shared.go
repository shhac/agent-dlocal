package shared

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/shhac/agent-dlocal/internal/api"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
	"github.com/shhac/agent-dlocal/internal/output"
	libcli "github.com/shhac/lib-agent-cli/cli"
)

// GlobalFlags holds the family's shared persistent flags plus agent-dlocal's
// domain flags. dLocal keys carry no prefix that identifies live vs sandbox, so
// the environment is a HOST distinction: it rides on the profile as non-secret
// metadata and is overridable per-invocation with --base-url.
type GlobalFlags struct {
	libcli.Globals // Format, TimeoutMS, Debug, Color, Expose

	Profile    string
	BaseURL    string
	MaxRetries int
	// Country overrides the profile's country for this invocation. Every
	// country-taking command reads it, so switching market is one flag rather
	// than a different spelling per command.
	Country string
}

// ResolveCountry applies the precedence: an explicit argument, then --country,
// then the profile's stored country. Commands that accept countries positionally
// pass those first.
func (g *GlobalFlags) ResolveCountry(explicit, profileCountry string) string {
	return firstNonEmpty(explicit, g.Country, profileCountry)
}

type GlobalsFunc = func() *GlobalFlags

func ToAnySlice[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

// GetEntities runs the multi-capable get contract: one session, each id
// resolved through getOne, streamed per the shared contract (NDJSON by default
// — one record or {"@unresolved":…} per id in input order; item-level misses
// stay on stdout, command-level failures bubble to the single sink).
func GetEntities(flags *GlobalFlags, host Host, args []string, getOne func(ctx context.Context, session *Session, id string) (any, error)) error {
	return WithSession(flags, host, func(ctx context.Context, session *Session) error {
		return libcli.EntityGet(output.Stdout(), flags.Format, args, func(id string) (any, error) {
			return getOne(ctx, session, id)
		})
	})
}

func WriteList(items []any, format string) {
	f := output.ResolveFormat(format, output.FormatNDJSON)
	if f == output.FormatNDJSON {
		w := output.NewNDJSONWriter(output.Stdout())
		for _, item := range items {
			_ = w.WriteItem(item)
		}
		return
	}
	output.Print(map[string]any{"data": items}, f, true)
}

func WriteItem(data any, format string) {
	f := output.ResolveFormat(format, output.FormatJSON)
	output.Print(data, f, true)
}

func RedactionOptions(flags *GlobalFlags) output.RedactionOptions {
	if flags == nil {
		return output.RedactionOptions{}
	}
	return output.RedactionOptions{Expose: flags.Expose}
}

// RequireFlag returns nil when value is present, or a structured
// fixable_by:agent error when it is empty.
func RequireFlag(flag, value, hint string) error {
	if value != "" {
		return nil
	}
	err := agenterrors.Newf(agenterrors.FixableByAgent, "--%s is required", flag)
	if hint != "" {
		err = err.WithHint(hint)
	}
	return err
}

func ContextWithTimeout(parent context.Context, ms int) (context.Context, context.CancelFunc) {
	if ms <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, time.Duration(ms)*time.Millisecond)
}

// FetchItem retrieves path, decodes the response, and applies the expose-aware
// redaction policy so an EntityGet resolver can hand it back to the stream.
func FetchItem(ctx context.Context, client *api.Client, flags *GlobalFlags, path string, params url.Values) (any, error) {
	raw, err := client.Get(ctx, path, params)
	if err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByAgent)
	}
	return output.Redact(data, RedactionOptions(flags)), nil
}

// countJSONArray counts the elements of a JSON array response without decoding
// it into a typed shape.
func countJSONArray(raw json.RawMessage) int {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return 0
	}
	return len(items)
}

func WriteDebug(fields map[string]any) {
	enc := json.NewEncoder(output.Stderr())
	enc.SetEscapeHTML(false)
	_ = enc.Encode(fields)
}
