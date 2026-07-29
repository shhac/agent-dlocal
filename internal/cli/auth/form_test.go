package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/agent-dlocal/internal/credential"
	"github.com/shhac/lib-agent-cli/dialog"
	"github.com/shhac/lib-agent-cli/dialog/dialogtest"
)

// answerer replays one canned value per Prompt call, so a chain of
// single-field dialogs can be driven in order.
type answerer struct {
	dialogtest.Recorder
	values []string
	calls  int
}

func (a *answerer) Prompt(ctx context.Context, spec dialog.Spec) ([]dialog.Result, error) {
	a.Calls = append(a.Calls, spec)
	if a.AvailableErr != nil {
		return nil, a.AvailableErr
	}
	value := ""
	if a.calls < len(a.values) {
		value = a.values[a.calls]
	}
	a.calls++
	return []dialog.Result{{ID: spec.Items[0].ID, Value: value}}, nil
}

func withRecorder(t *testing.T, values ...string) *answerer {
	t.Helper()
	rec := &answerer{values: values}
	restore := dialog.SetDefault(rec)
	t.Cleanup(restore)
	return rec
}

// The bug this guards: the zenity backend renders a Password field as a
// generic "Password:" box and DISCARDS Field.Label, so a chain of three
// password prompts is indistinguishable to the user. The title is the only
// text that reaches the screen, so every prompt's title must name the secret
// it is asking for.
func TestEachPromptTitleNamesTheSecret(t *testing.T) {
	rec := withRecorder(t, "l", "t", "s")

	if _, err := promptCredentialsViaDialog(context.Background(), "ldt-payins", credential.Set{}); err != nil {
		t.Fatalf("promptCredentialsViaDialog: %v", err)
	}

	if len(rec.Calls) != 3 {
		t.Fatalf("expected one dialog per secret, got %d", len(rec.Calls))
	}
	for i, want := range []string{"X-Login", "X-Trans-Key", "Secret key"} {
		title := rec.Calls[i].Title
		if !strings.Contains(title, want) {
			t.Errorf("prompt %d title does not name the secret it wants.\n  title: %q\n  want it to contain: %q", i+1, title, want)
		}
		if !strings.Contains(title, "ldt-payins") {
			t.Errorf("prompt %d title does not name the profile: %q", i+1, title)
		}
	}
}

// Each Spec must carry exactly one field. A multi-item Spec is rendered by the
// backend as an unlabelled "(step N of M)" chain, which is the failure mode
// this design exists to avoid.
func TestEachPromptCarriesExactlyOneField(t *testing.T) {
	rec := withRecorder(t, "l", "t", "s")

	if _, err := promptCredentialsViaDialog(context.Background(), "prod", credential.Set{}); err != nil {
		t.Fatalf("promptCredentialsViaDialog: %v", err)
	}
	for i, call := range rec.Calls {
		if len(call.Items) != 1 {
			t.Fatalf("prompt %d carried %d fields; a multi-item Spec loses the labels", i+1, len(call.Items))
		}
		if call.Items[0].InputType != dialog.Password {
			t.Fatalf("prompt %d is not masked: field %q would show the secret on screen", i+1, call.Items[0].ID)
		}
	}
}

func TestPromptsFillEverySecret(t *testing.T) {
	withRecorder(t, "login123", "trans456", "secret789")

	got, err := promptCredentialsViaDialog(context.Background(), "prod", credential.Set{})
	if err != nil {
		t.Fatalf("promptCredentialsViaDialog: %v", err)
	}

	want := credential.Set{Login: "login123", TransKey: "trans456", SecretKey: "secret789"}
	if got != want {
		t.Fatalf("collected set = %+v, want %+v", got, want)
	}
}

// A value already supplied by flag must not be asked for again.
func TestPromptsSkipFieldsAlreadySupplied(t *testing.T) {
	rec := withRecorder(t, "trans456", "secret789")

	got, err := promptCredentialsViaDialog(context.Background(), "prod", credential.Set{Login: "from-flag"})
	if err != nil {
		t.Fatalf("promptCredentialsViaDialog: %v", err)
	}

	if len(rec.Calls) != 2 {
		t.Fatalf("expected 2 prompts for the 2 missing secrets, got %d", len(rec.Calls))
	}
	if strings.Contains(rec.Calls[0].Title, "X-Login") {
		t.Errorf("prompted for a secret already supplied by flag: %q", rec.Calls[0].Title)
	}
	if got.Login != "from-flag" {
		t.Errorf("flag-supplied login was overwritten: %q", got.Login)
	}
}

// A headless environment must fail before any prompt, with a hint naming the
// non-interactive fallback — an agent cannot open a dialog and needs to be
// told what to do instead.
func TestHeadlessFailureNamesTheFallback(t *testing.T) {
	rec := &answerer{}
	rec.AvailableErr = dialog.ErrNoGUI
	restore := dialog.SetDefault(rec)
	t.Cleanup(restore)

	_, err := promptCredentialsViaDialog(context.Background(), "prod", credential.Set{})
	if err == nil {
		t.Fatal("expected an error when no GUI is available")
	}
	if len(rec.Calls) != 0 {
		t.Fatalf("prompted %d times despite no GUI", len(rec.Calls))
	}
	if !strings.Contains(err.Error(), "no GUI") && !strings.Contains(err.Error(), "GUI") {
		t.Fatalf("unexpected error: %v", err)
	}
}
