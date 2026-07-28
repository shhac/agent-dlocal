package shared

import (
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
