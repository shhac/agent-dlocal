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
}

type GlobalsFunc = func() *GlobalFlags

func ToAnySlice[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

// GetEntities runs the multi-capable get contract: one client, each id resolved
// through getOne, streamed per the shared contract (NDJSON by default — one
// record or {"@unresolved":…} per id in input order; item-level misses stay on
// stdout with exit 0, command-level failures bubble to the single sink).
func GetEntities(flags *GlobalFlags, args []string, getOne func(ctx context.Context, client *api.Client, id string) (any, error)) error {
	return WithClient(flags, func(ctx context.Context, client *api.Client) error {
		return libcli.EntityGet(output.Stdout(), flags.Format, args, func(id string) (any, error) {
			return getOne(ctx, client, id)
		})
	})
}

// GetPayoutEntities is GetEntities against the payouts host, which is a
// separate service with its own signer.
func GetPayoutEntities(flags *GlobalFlags, args []string, getOne func(ctx context.Context, client *api.Client, id string) (any, error)) error {
	return WithPayoutsClient(flags, func(ctx context.Context, client *api.Client) error {
		return libcli.EntityGet(output.Stdout(), flags.Format, args, func(id string) (any, error) {
			return getOne(ctx, client, id)
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

func WriteDebug(fields map[string]any) {
	enc := json.NewEncoder(output.Stderr())
	enc.SetEscapeHTML(false)
	_ = enc.Encode(fields)
}

func AddString(values url.Values, key, value string) {
	if value != "" {
		values.Set(key, value)
	}
}
