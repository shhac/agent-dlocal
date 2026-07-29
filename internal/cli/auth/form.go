package auth

import (
	"context"
	"fmt"

	"github.com/shhac/agent-dlocal/internal/credential"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
	"github.com/shhac/lib-agent-cli/dialog"
)

// credentialField pairs a dialog field with the setter that files its value
// into the credential set, so adding a secret is one table entry.
type credentialField struct {
	id    string
	label string
	// needed reports whether this secret is still missing, so a value already
	// supplied by flag is not prompted for again.
	needed func(credential.Set) bool
	assign func(*credential.Set, string)
}

var credentialFields = []credentialField{
	{
		id: "login", label: "X-Login",
		needed: func(s credential.Set) bool { return s.Login == "" },
		assign: func(s *credential.Set, v string) { s.Login = v },
	},
	{
		id: "trans_key", label: "X-Trans-Key",
		needed: func(s credential.Set) bool { return s.TransKey == "" },
		assign: func(s *credential.Set, v string) { s.TransKey = v },
	},
	{
		id: "secret_key", label: "Secret key (used to sign requests)",
		needed: func(s credential.Set) bool { return s.SecretKey == "" },
		assign: func(s *credential.Set, v string) { s.SecretKey = v },
	},
}

// promptCredentialsViaDialog collects the missing secrets through native OS
// dialogs, so the values go from the user's keyboard into the keychain without
// passing through the transcript or the model's context.
//
// It prompts ONE FIELD PER CALL, with the field name in the dialog TITLE.
// That is not a style choice — it is forced by the backend:
//
//   - dialog.Prompt already renders a multi-item Spec as a CHAIN of dialogs
//     (one per field, titled "(step N of M)"), not as a single combined form.
//     Passing all four items at once therefore buys nothing.
//   - For a Password field with no Initial value, the zenity backend calls
//     zenity.Password, which renders a generic "Password:" body and DISCARDS
//     Field.Label entirely. The label survives only in error messages.
//
// So with a multi-item Spec the user sees three identical "Password:" boxes
// numbered 1..3 with no indication of which secret each one wants. The title is
// the only channel that reaches the screen, so each field gets its own
// single-item Spec and a self-describing title.
//
// The client certificate is deliberately not collected here. dialog.InputType
// is Text | Password — single-line entries — and neither a PEM blob nor a .jks
// can sanely be typed into one. A path is also not a secret, so --cert/--key
// take paths.
//
// Nor is a key passphrase collected. There used to be a fourth field for one,
// but nothing ever read it: mTLS goes through tls.LoadX509KeyPair, which cannot
// decrypt an encrypted key. Prompting for a secret and then discarding it is
// worse than not supporting the feature, so the client key must be unencrypted
// PEM and the error hint says so.
func promptCredentialsViaDialog(ctx context.Context, profile string, set credential.Set) (credential.Set, error) {
	pending := make([]credentialField, 0, len(credentialFields))
	for _, field := range credentialFields {
		if field.needed(set) {
			pending = append(pending, field)
		}
	}
	if len(pending) == 0 {
		return set, nil
	}

	if err := dialog.Default.Available(); err != nil {
		return set, classifyDialogErr(err, profile)
	}

	for i, field := range pending {
		spec := dialog.Spec{
			Title: promptTitle(profile, field.label, i, len(pending)),
			Items: []dialog.Field{{ID: field.id, Label: field.label, InputType: dialog.Password}},
		}

		results, err := dialog.Default.Prompt(ctx, spec)
		if err != nil {
			return set, classifyDialogErr(err, profile)
		}
		for _, result := range results {
			if result.ID == field.id {
				field.assign(&set, result.Value)
			}
		}
	}
	return set, nil
}

// promptTitle names the profile, the secret being asked for, and the position
// in the chain. Since the password dialog's body is a fixed "Password:", this
// title is the user's only cue about which value to paste.
func promptTitle(profile, label string, index, total int) string {
	if total == 1 {
		return fmt.Sprintf("agent-dlocal · %s · %s", profile, label)
	}
	return fmt.Sprintf("agent-dlocal · %s · %s (%d of %d)", profile, label, index+1, total)
}

func classifyDialogErr(err error, profile string) error {
	cat, hint := dialog.ClassifyError(err)
	switch cat {
	case dialog.CategoryHuman:
		// A headless agent cannot open a dialog, so the hint has to name the
		// escape route rather than just reporting that the dialog failed.
		hint = "agent-dlocal auth add --form requires a graphical desktop session. " +
			"Ask the user to run it on their local machine, or fall back to non-interactive: " +
			fmt.Sprintf("agent-dlocal auth add %s --login <x-login> --trans-key <x-trans-key> --secret-key <secret>", profile)
	case dialog.CategoryRetry:
		hint = "User cancelled the dialog. Re-run agent-dlocal auth add --form to retry."
	}
	return agenterrors.Wrap(err, categoryToFixableBy(cat)).WithHint(hint)
}

func categoryToFixableBy(c dialog.Category) agenterrors.FixableBy {
	switch c {
	case dialog.CategoryHuman:
		return agenterrors.FixableByHuman
	case dialog.CategoryRetry:
		return agenterrors.FixableByRetry
	default:
		return agenterrors.FixableByAgent
	}
}
