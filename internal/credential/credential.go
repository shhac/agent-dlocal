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
	// 0700, not 0755: on the fallback path this directory holds credentials.json
	// with real secrets in it, and a 0600 file inside a world-readable directory
	// still leaks its existence and size. lib-agent-cli's creds.Store uses 0700
	// for the same reason.
	//
	// Chmod as well as MkdirAll, because the directory is usually created first
	// by config.Write and MkdirAll will not tighten one that already exists —
	// including one left 0755 by an earlier version.
	dir := config.ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
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
	} else {
		// The keychain refused, so this write lands in the file index. Any
		// item a PREVIOUS store left in the keychain still holds the old
		// secrets, and once the index says file-backed nothing will ever
		// delete it — Remove only clears what the index claims. Clear it here
		// so a backend transition cannot strand a live credential.
		_ = keychain.Delete(name)
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

	// Delete unconditionally rather than only when the index claims the
	// keychain owns this profile: the index can be wrong in the safe-to-fix
	// direction (a keychain item from an earlier store), and an extra delete of
	// something absent is a no-op.
	if err := keychain.Delete(name); err != nil && entry.KeychainManaged {
		return err
	}

	delete(index, name)
	return writeIndex(index)
}
