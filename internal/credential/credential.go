// Package credential stores one dLocal credential set per profile.
//
// A dLocal credential set is not a single string: it is X-Login, X-Trans-Key,
// and a Secret key used only for signing. All three are marshalled into ONE
// opaque keychain item per profile, rather than
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
	"github.com/shhac/lib-agent-cli/creds"
)

// keychainSentinel is stored in the index in place of the real credential blob
// when the secrets live in the OS keychain. When the keychain is unavailable
// (non-macOS, or opted out via AGENT_DLOCAL_NO_KEYCHAIN / LIB_AGENT_NO_KEYCHAIN)
// the blob is kept in the 0600 index file instead.
const keychainSentinel = "__KEYCHAIN__"

// Set is one merchant's credentials. It is only ever handled as a whole: read
// from the backend, handed to the signer, discarded.
type Set struct {
	Login     string `json:"login"`
	TransKey  string `json:"trans_key"`
	SecretKey string `json:"secret_key"`
}

// Complete reports whether all three secrets are present.
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

// store is the credential index's file: 0600 writes into a 0700 parent, atomic
// replacement by rename, and Update for a locked read-modify-write. This was
// hand-rolled with os.ReadFile/os.WriteFile, which carried a lost-update race —
// two concurrent writers each built their write from a snapshot taken before the
// other landed, and the loser's entry simply vanished.
//
// That is worse here than a lost write usually is: the secrets are already in
// the keychain by the time the index write happens, so a dropped entry strands a
// live credential that `auth list` can no longer show and `auth remove` can no
// longer look up.
func store() creds.Store {
	return creds.Store{Path: credentialsPath()}
}

func readIndex() (map[string]credentialEntry, error) {
	index := make(map[string]credentialEntry)
	if err := store().Load(&index); err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]credentialEntry)
	}
	return index, nil
}

// updateIndex applies mutate to the index under the store's exclusive lock, so
// concurrent `auth add`/`auth remove` invocations serialize rather than
// clobbering each other. Returning an error from mutate aborts without writing.
func updateIndex(mutate func(index map[string]credentialEntry) error) error {
	index := make(map[string]credentialEntry)
	return store().Update(&index, func() error {
		if index == nil {
			index = make(map[string]credentialEntry)
		}
		if err := tightenConfigDir(); err != nil {
			return err
		}
		return mutate(index)
	})
}

// tightenConfigDir narrows a config directory an earlier version may have left
// 0755. On the fallback path it holds credentials.json with real secrets in it,
// and a 0600 file inside a world-readable directory still leaks its existence
// and size. creds.Store creates the directory 0700, but MkdirAll will not
// tighten one that already exists — and config.Write usually creates it first.
func tightenConfigDir() error {
	return os.Chmod(config.ConfigDir(), 0o700)
}

// Store persists a credential set as one opaque blob. It prefers the OS
// keychain; when the keychain is unavailable it keeps the blob in the 0600
// index file instead. Returns "keychain" or "file" so the caller can surface
// which backend took it.
func Store(name string, set Set) (string, error) {
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

	// The index write is the step that must not race: the keychain already holds
	// the secret by now, so an entry lost to a concurrent writer leaves that
	// secret referenced by nothing.
	if err := updateIndex(func(index map[string]credentialEntry) error {
		index[name] = entry
		return nil
	}); err != nil {
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
	return updateIndex(func(index map[string]credentialEntry) error {
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
		return nil
	})
}
