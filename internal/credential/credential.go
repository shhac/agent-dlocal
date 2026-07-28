// Package credential stores one dLocal credential set per profile.
//
// A dLocal credential set is not a single string: it is X-Login, X-Trans-Key, a
// Secret key used only for signing, and optionally a client-key passphrase. All
// of them are marshalled into ONE opaque keychain item per profile, rather than
// suffixed items (profile.login, profile.secret, ...). One item means one
// Delete: there is no partial-write window and no way for a Remove to miss a
// suffix and leave a live secret behind.
//
// Nothing in this package returns a printable view of the secrets. The signer
// reads a Set; everything else deals in profile names.
package credential

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shhac/agent-dlocal/internal/config"
)

// keychainSentinel is stored in the index in place of the real credential blob
// when the secrets live in the OS keychain. When the keychain is unavailable
// (non-macOS, or opted out via AGENT_DLOCAL_NO_KEYCHAIN / LIB_AGENT_NO_KEYCHAIN)
// the blob is kept in the 0600 index file instead.
const keychainSentinel = "__KEYCHAIN__"

// Set is one merchant's credentials. It is only ever handled as a whole: read
// from the backend, handed to the signer, discarded.
type Set struct {
	Login         string `json:"login"`
	TransKey      string `json:"trans_key"`
	SecretKey     string `json:"secret_key"`
	KeyPassphrase string `json:"key_passphrase,omitempty"`
}

// Complete reports whether the three required secrets are present. The key
// passphrase is optional — it only matters when mTLS is configured.
func (s Set) Complete() bool {
	return s.Login != "" && s.TransKey != "" && s.SecretKey != ""
}

// Missing names the absent required fields, for a hint that tells the caller
// what to supply. It names FIELDS, never values.
func (s Set) Missing() []string {
	var missing []string
	if s.Login == "" {
		missing = append(missing, "login")
	}
	if s.TransKey == "" {
		missing = append(missing, "trans-key")
	}
	if s.SecretKey == "" {
		missing = append(missing, "secret-key")
	}
	return missing
}

type credentialEntry struct {
	Blob            string `json:"blob,omitempty"`
	KeychainManaged bool   `json:"keychain_managed"`
}

type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("profile credential %q not found", e.Name)
}

func credentialsPath() string {
	return filepath.Join(config.ConfigDir(), "credentials.json")
}

func readIndex() (map[string]credentialEntry, error) {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]credentialEntry), nil
		}
		return nil, err
	}
	var index map[string]credentialEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]credentialEntry)
	}
	return index, nil
}

func writeIndex(index map[string]credentialEntry) error {
	dir := config.ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(credentialsPath(), append(data, '\n'), 0o600)
}

// Store persists a credential set as one opaque blob. It prefers the OS
// keychain; when the keychain is unavailable it keeps the blob in the 0600
// index file instead. Returns "keychain" or "file" so the caller can surface
// which backend took it.
func Store(name string, set Set) (string, error) {
	index, err := readIndex()
	if err != nil {
		return "", err
	}

	blob, err := json.Marshal(set)
	if err != nil {
		return "", err
	}

	entry := credentialEntry{Blob: string(blob)}
	storage := "file"
	if err := keychain.Store(name, string(blob)); err == nil {
		entry.Blob = keychainSentinel
		entry.KeychainManaged = true
		storage = "keychain"
	}

	index[name] = entry
	if err := writeIndex(index); err != nil {
		return "", err
	}
	return storage, nil
}

func Get(name string) (Set, error) {
	index, err := readIndex()
	if err != nil {
		return Set{}, err
	}
	entry, ok := index[name]
	if !ok {
		return Set{}, &NotFoundError{Name: name}
	}

	blob := entry.Blob
	if entry.KeychainManaged {
		blob, err = keychain.Get(name)
		if err != nil {
			return Set{}, err
		}
	}

	var set Set
	if err := json.Unmarshal([]byte(blob), &set); err != nil {
		return Set{}, fmt.Errorf("stored credential for profile %q is not readable", name)
	}
	return set, nil
}

// Storage reports which backend holds a profile's secrets, without touching the
// secrets themselves. Used by `auth list`, which must never read a value.
func Storage(name string) (string, error) {
	index, err := readIndex()
	if err != nil {
		return "", err
	}
	entry, ok := index[name]
	if !ok {
		return "", &NotFoundError{Name: name}
	}
	if entry.KeychainManaged {
		return "keychain", nil
	}
	return "file", nil
}

func Remove(name string) error {
	index, err := readIndex()
	if err != nil {
		return err
	}
	entry, ok := index[name]
	if !ok {
		return &NotFoundError{Name: name}
	}

	if entry.KeychainManaged {
		if err := keychain.Delete(name); err != nil {
			return err
		}
	}

	delete(index, name)
	return writeIndex(index)
}
