package auth

import (
	"context"
	"fmt"

	"github.com/shhac/agent-dlocal/internal/credential"
	agenterrors "github.com/shhac/agent-dlocal/internal/errors"
	"github.com/shhac/lib-agent-cli/dialog"
)

// promptCredentialsViaDialog collects every secret in ONE native dialog.
//
// dialog.Spec.Items is already a slice, so a four-field prompt needs no library
// change. One dialog rather than four also means one cancel: a user who backs
// out does not leave a half-written credential behind.
//
// Fields already supplied by flag are skipped, so `--login x --form` prompts
// only for what is still missing.
//
// The client certificate is deliberately NOT a field here. dialog.InputType is
// Text | Password — single-line entries — and neither a PEM blob nor a .jks
// can sanely be typed into one. A path is also not a secret, so --cert/--key
// take paths and the file stays under the user's own permissions. The key
// PASSPHRASE is a field: short, single-line, and genuinely secret.
func promptCredentialsViaDialog(ctx context.Context, profile string, set credential.Set, wantPassphrase bool) (credential.Set, error) {
	items := make([]dialog.Field, 0, 4)
	if set.Login == "" {
		items = append(items, dialog.Field{ID: "login", Label: "X-Login", InputType: dialog.Password})
	}
	if set.TransKey == "" {
		items = append(items, dialog.Field{ID: "trans_key", Label: "X-Trans-Key", InputType: dialog.Password})
	}
	if set.SecretKey == "" {
		items = append(items, dialog.Field{ID: "secret_key", Label: "Secret key (signing)", InputType: dialog.Password})
	}
	if wantPassphrase && set.KeyPassphrase == "" {
		items = append(items, dialog.Field{ID: "key_passphrase", Label: "Client key passphrase", InputType: dialog.Password})
	}
	if len(items) == 0 {
		return set, nil
	}

	if err := dialog.Default.Available(); err != nil {
		return set, classifyDialogErr(err, profile)
	}

	results, err := dialog.Default.Prompt(ctx, dialog.Spec{
		Title: fmt.Sprintf("agent-dlocal credentials: %s", profile),
		Items: items,
	})
	if err != nil {
		return set, classifyDialogErr(err, profile)
	}

	for _, result := range results {
		switch result.ID {
		case "login":
			set.Login = result.Value
		case "trans_key":
			set.TransKey = result.Value
		case "secret_key":
			set.SecretKey = result.Value
		case "key_passphrase":
			set.KeyPassphrase = result.Value
		}
	}
	return set, nil
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
