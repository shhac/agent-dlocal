package credential

import (
	"fmt"

	"github.com/shhac/lib-agent-cli/creds"
)

const keychainService = "app.paulie.agent-dlocal"

// MCPKeychainService is the Keychain service for the MCP server's local-OAuth
// secrets — the CLI's service plus a ".mcp" namespace, separate from the API creds.
func MCPKeychainService() string { return keychainService + ".mcp" }

// credsKeychain adapts creds.Keychain to the keychainBackend interface, keeping
// the error-returning Get/Store/Delete contract the credential index relies on.
type credsKeychain struct {
	kc *creds.Keychain
}

func newCredsKeychain() credsKeychain {
	return credsKeychain{kc: creds.NewKeychain(keychainService)}
}

func (k credsKeychain) Store(name, blob string) error {
	return k.kc.Set(name, blob)
}

func (k credsKeychain) Get(name string) (string, error) {
	if !k.kc.Available() {
		return "", creds.ErrKeychainUnavailable
	}
	value, ok := k.kc.Get(name)
	if !ok {
		return "", fmt.Errorf("keychain credential %q not found", name)
	}
	return value, nil
}

func (k credsKeychain) Delete(name string) error {
	if !k.kc.Available() {
		return nil
	}
	return k.kc.Delete(name)
}
