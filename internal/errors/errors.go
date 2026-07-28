// Package errors re-exports the shared error contract from lib-agent-output so
// the rest of agent-dlocal keeps the internal/errors import path while the
// implementation lives in one place. (Migration shim — call sites can later be
// pointed at lib-agent-output directly and this package deleted.)
package errors

import (
	out "github.com/shhac/lib-agent-output"
)

type (
	FixableBy = out.FixableBy
	// APIError is the family name for the shared output.Error type. It keeps the
	// WithHint/WithCause chaining and the error/fixable_by/hint JSON contract.
	APIError = out.Error
)

const (
	FixableByAgent = out.FixableByAgent
	FixableByHuman = out.FixableByHuman
	FixableByRetry = out.FixableByRetry
)

var (
	New  = out.New
	Newf = out.Newf
	Wrap = out.Wrap
)
