package output

import (
	"slices"
	"strings"

	out "github.com/shhac/lib-agent-output"
)

// RedactionOptions carries the per-call --expose allowlist. agent-dlocal threads
// the global --expose flag through each Redact call rather than a package global.
type RedactionOptions struct {
	Expose []string
}

// RedactedString is the masked-value placeholder. It matches the shared
// out.RedactedPlaceholder so callers and tests can refer to either.
const RedactedString = out.RedactedPlaceholder

// ParseExpose splits comma-joined --expose entries into normalized tokens.
func ParseExpose(values []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			normalized := normalizeExpose(part)
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			result = append(result, normalized)
		}
	}
	return result
}

// Redact masks agent-dlocal's sensitive fields using the shared redaction
// MECHANISM (the walk, the [REDACTED] placeholder, the @redacted notes, and
// --expose matching all live in lib-agent-output). What stays here is the
// POLICY — dlocalSecrets decides WHICH fields are secret.
func Redact(data any, opts RedactionOptions) any {
	cleaned, ok := toCleanAny(data, false)
	if !ok {
		return data
	}
	return out.Redact(cleaned, dlocalSecrets(), opts.Expose)
}

func dlocalSecrets() out.RedactRule {
	return func(field out.RedactField) bool {
		return shouldRedactField(field.Key, field.Path)
	}
}

// piiSubtrees are response branches that are PII end to end. dLocal carries far
// more raw identity data than Stripe — payer.document is a national ID number
// (CPF, CUIT, DNI, ...), directly identifying and legally sensitive across
// dLocal's markets — so these subtrees are masked wholesale rather than by
// enumerating every leaf a country-specific schema might add.
var piiSubtrees = []string{
	"payer.address",
	"beneficiary.address",
	"bank_account",
	"beneficiary.bank_account",
}

// piiKeys are identity fields wherever they appear.
var piiKeys = map[string]bool{
	"document":       true,
	"document_type":  true,
	"email":          true,
	"phone":          true,
	"user_reference": true,
	"device_id":      true,
	"ip":             true,
	"cvv":            true,
	"number":         true, // card PAN, and street number under an address
}

// nameContexts are the paths where a "name" field is a person, not a product.
// A payment method's name ("Smart Pix") must survive; a payer's must not.
var nameContexts = []string{"payer", "beneficiary", "card", "holder"}

func shouldRedactField(key, path string) bool {
	k := strings.ToLower(key)
	p := strings.ToLower(path)

	for _, subtree := range piiSubtrees {
		if pathWithin(p, subtree) {
			return true
		}
	}

	if piiKeys[k] {
		return true
	}

	if k == "name" || k == "holder_name" || k == "first_name" || k == "last_name" {
		for _, ctx := range nameContexts {
			if strings.HasPrefix(p, ctx+".") || strings.Contains(p, "."+ctx+".") {
				return true
			}
		}
		return false
	}

	// The family's substring rules, plus the two dLocal request-signing values:
	// a leaked signature is an oracle, and trans_key is a bare credential.
	return strings.Contains(k, "secret") ||
		strings.Contains(k, "password") ||
		strings.Contains(k, "passphrase") ||
		strings.Contains(k, "token") ||
		strings.Contains(k, "api_key") ||
		strings.Contains(k, "trans_key") ||
		strings.Contains(k, "trans-key") ||
		k == "x-login" ||
		k == "login" ||
		strings.Contains(k, "signature") ||
		k == "authorization"
}

// pathWithin reports whether path sits at or below subtree, matching on whole
// segments anywhere in the path rather than only at its start.
//
// Anchoring at the start was a real leak. The shared walker prefixes array
// elements with a "[]" segment and `investigate` nests records under
// "evidence.payment", so the paths that actually reach this rule look like
// "[].payer.address.city" and "evidence.payment.payer.address.city" — neither
// of which starts with "payer.address". The key-based rules masked name, email
// and document, which made the gap look closed while the whole address block
// went out in clear.
func pathWithin(path, subtree string) bool {
	segments := splitPathSegments(path)
	want := strings.Split(subtree, ".")

	for i := 0; i+len(want) <= len(segments); i++ {
		if slices.Equal(segments[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// splitPathSegments drops the walker's "[]" array markers so an element's path
// compares equal to the same field outside an array.
func splitPathSegments(path string) []string {
	raw := strings.Split(path, ".")
	segments := make([]string, 0, len(raw))
	for _, segment := range raw {
		if segment = strings.TrimSuffix(segment, "[]"); segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func normalizeExpose(value string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
}
