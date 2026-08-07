package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shhac/agent-dlocal/internal/config"
)

// fakeKeychain stands in for the OS keychain so the round-trip is testable off
// a Mac and without touching the developer's real keychain.
//
// The mutex is not incidental: the concurrency tests drive twenty writers
// through Store, and an unguarded map here would trip the race detector on the
// double rather than on the code under test.
type fakeKeychain struct {
	mu        sync.Mutex
	items     map[string]string
	failStore bool
}

func newFakeKeychain() *fakeKeychain {
	return &fakeKeychain{items: map[string]string{}}
}

func (f *fakeKeychain) Store(name, blob string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStore {
		return os.ErrPermission
	}
	f.items[name] = blob
	return nil
}

func (f *fakeKeychain) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.items[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func (f *fakeKeychain) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, name)
	return nil
}

func (f *fakeKeychain) setFailStore(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStore = fail
}

func (f *fakeKeychain) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return keysOf(f.items)
}

func withTestEnv(t *testing.T) *fakeKeychain {
	t.Helper()
	dir := t.TempDir()
	config.SetConfigDir(dir)
	t.Cleanup(func() { config.SetConfigDir("") })

	kc := newFakeKeychain()
	restore := setKeychainBackendForTest(kc)
	t.Cleanup(restore)
	return kc
}

func sampleSet() Set {
	return Set{
		Login:     "login123",
		TransKey:  "trans456",
		SecretKey: "secret789",
	}
}

func TestStoreGetRoundTripsAllFourSecrets(t *testing.T) {
	withTestEnv(t)

	storage, err := Store("prod", sampleSet())
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if storage != "keychain" {
		t.Fatalf("storage = %q, want keychain", storage)
	}

	got, err := Get("prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != sampleSet() {
		t.Fatalf("round trip mismatch: got %+v", got)
	}
}

// The whole point of the one-opaque-item design: a single Delete removes every
// secret. A suffixed scheme could leave one behind.
func TestRemoveDeletesTheSingleKeychainItem(t *testing.T) {
	kc := withTestEnv(t)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if len(kc.names()) != 1 {
		t.Fatalf("expected exactly 1 keychain item, got %d: %v", len(kc.names()), kc.names())
	}

	if err := Remove("prod"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(kc.names()) != 0 {
		t.Fatalf("keychain still holds %d item(s) after Remove: %v", len(kc.names()), kc.names())
	}

	if _, err := Get("prod"); err == nil {
		t.Fatal("Get after Remove: want error, got nil")
	}
}

// When the keychain refuses, the blob falls back to the 0600 index file. The
// index must never be world-readable, since in that mode it holds real secrets.
func TestFileFallbackIsOwnerOnly(t *testing.T) {
	kc := withTestEnv(t)
	kc.setFailStore(true)

	storage, err := Store("prod", sampleSet())
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if storage != "file" {
		t.Fatalf("storage = %q, want file", storage)
	}

	info, err := os.Stat(filepath.Join(config.ConfigDir(), "credentials.json"))
	if err != nil {
		t.Fatalf("stat credentials.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials.json mode = %o, want 600", perm)
	}

	got, err := Get("prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != sampleSet() {
		t.Fatalf("file-fallback round trip mismatch: got %+v", got)
	}
}

// In keychain mode the index is a pointer, not a copy: no secret value may
// appear in it.
func TestIndexHoldsNoSecretWhenKeychainManaged(t *testing.T) {
	withTestEnv(t)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(config.ConfigDir(), "credentials.json"))
	if err != nil {
		t.Fatalf("read credentials.json: %v", err)
	}
	for _, secret := range []string{"login123", "trans456", "secret789"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("credentials.json leaked %q:\n%s", secret, data)
		}
	}

	var index map[string]credentialEntry
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if !index["prod"].KeychainManaged || index["prod"].Blob != keychainSentinel {
		t.Fatalf("index entry = %+v, want sentinel + keychain_managed", index["prod"])
	}
}

func TestGetUnknownProfileIsNotFound(t *testing.T) {
	withTestEnv(t)

	_, err := Get("nope")
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Get unknown profile: want *NotFoundError, got %T (%v)", err, err)
	}
}

func TestMissingNamesFieldsNotValues(t *testing.T) {
	set := Set{TransKey: "trans456"}
	missing := set.Missing()

	if set.Complete() {
		t.Fatal("Complete() = true for a set with no login or secret key")
	}
	want := []string{"login", "secret-key"}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("Missing() = %v, want %v", missing, want)
	}
	for _, name := range missing {
		if strings.Contains(name, "trans456") {
			t.Fatalf("Missing() leaked a value: %q", name)
		}
	}
}

func TestStorageReportsBackendWithoutReadingSecrets(t *testing.T) {
	withTestEnv(t)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}
	storage, err := Storage("prod")
	if err != nil {
		t.Fatalf("Storage: %v", err)
	}
	if storage != "keychain" {
		t.Fatalf("Storage = %q, want keychain", storage)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Re-storing a profile while the keychain is unavailable must not strand the
// previous keychain item. Without this, the index flips to file-backed, Remove
// only deletes keychain items it believes it owns, and the OLD secrets survive
// `auth remove` indefinitely — directly contradicting this package's stated
// one-item-one-delete guarantee.
func TestReStoreIntoFileBackendClearsTheOldKeychainItem(t *testing.T) {
	kc := withTestEnv(t)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if len(kc.names()) != 1 {
		t.Fatalf("expected the first Store to use the keychain, got %d items", len(kc.names()))
	}

	kc.setFailStore(true)
	rotated := Set{Login: "l2", TransKey: "t2", SecretKey: "s2"}
	if _, err := Store("prod", rotated); err != nil {
		t.Fatalf("Store after keychain became unavailable: %v", err)
	}

	if len(kc.names()) != 0 {
		t.Fatalf("the previous keychain item survived a file-backed re-store, still holding the old secrets: %v", kc.names())
	}
	got, err := Get("prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != rotated {
		t.Fatalf("Get returned %+v, want the rotated set", got)
	}
}

// Remove must clear a keychain item even when the index says the credential is
// file-backed — otherwise a backend transition leaves a secret nothing will
// ever delete.
func TestRemoveClearsAKeychainItemTheIndexNoLongerClaims(t *testing.T) {
	kc := withTestEnv(t)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}
	kc.setFailStore(true)
	if _, err := Store("prod", Set{Login: "l2", TransKey: "t2", SecretKey: "s2"}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := Remove("prod"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(kc.names()) != 0 {
		t.Fatalf("Remove left %d keychain item(s) behind: %v", len(kc.names()), kc.names())
	}
}

// The directory holds credentials.json, which contains real secrets whenever
// the keychain is unavailable. A 0600 file inside a 0755 directory is still a
// worse posture than it looks.
func TestConfigDirIsOwnerOnly(t *testing.T) {
	kc := withTestEnv(t)
	kc.setFailStore(true)

	if _, err := Store("prod", sampleSet()); err != nil {
		t.Fatalf("Store: %v", err)
	}

	info, err := os.Stat(config.ConfigDir())
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir mode = %o, want 700", perm)
	}
}

// Two `auth add` invocations at once each used to build their index write from
// a snapshot taken before the other landed, so all but the last were erased —
// with twenty concurrent writers, one entry survived. That is worse than a
// plain lost write: the secrets are already in the keychain, so a dropped entry
// strands a live credential that nothing references and `auth remove` can no
// longer find. Store now holds the index's lock across read, mutate and write.
func TestConcurrentStoreKeepsEveryCredential(t *testing.T) {
	kc := withTestEnv(t)

	const writers = 20

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("merchant-%02d", i)
			if _, err := Store(name, setFor(i)); err != nil {
				errs <- fmt.Errorf("Store(%q): %w", name, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	index, err := readIndex()
	if err != nil {
		t.Fatalf("readIndex: %v", err)
	}
	if len(index) != writers {
		t.Fatalf("%d of %d index entries survived concurrent writes: %v", len(index), writers, index)
	}

	// A surviving index entry is only worth anything if it still resolves to the
	// right secrets — the failure this guards against strands the keychain item.
	for i := range writers {
		name := fmt.Sprintf("merchant-%02d", i)
		got, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q) after concurrent writes: %v", name, err)
		}
		if got != setFor(i) {
			t.Fatalf("Get(%q) = %+v, want %+v", name, got, setFor(i))
		}
	}
	if len(kc.names()) != writers {
		t.Fatalf("keychain holds %d items for %d profiles", len(kc.names()), writers)
	}
}

// Removes race adds in practice (`auth remove` on one profile while another is
// being added). Neither may erase the other's entry.
func TestConcurrentStoreAndRemoveDoNotClobber(t *testing.T) {
	withTestEnv(t)

	const writers = 20

	// Half the profiles exist up front so their concurrent Removes have work to
	// do; the other half are added concurrently.
	for i := 1; i < writers; i += 2 {
		if _, err := Store(fmt.Sprintf("doomed-%02d", i), setFor(i)); err != nil {
			t.Fatalf("seed Store: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				name := fmt.Sprintf("merchant-%02d", i)
				if _, err := Store(name, setFor(i)); err != nil {
					errs <- fmt.Errorf("Store(%q): %w", name, err)
				}
				return
			}
			name := fmt.Sprintf("doomed-%02d", i)
			if err := Remove(name); err != nil {
				errs <- fmt.Errorf("Remove(%q): %w", name, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	index, err := readIndex()
	if err != nil {
		t.Fatalf("readIndex: %v", err)
	}
	if len(index) != writers/2 {
		t.Fatalf("index holds %d entries, want %d: %v", len(index), writers/2, index)
	}
	for i := 0; i < writers; i += 2 {
		if _, ok := index[fmt.Sprintf("merchant-%02d", i)]; !ok {
			t.Fatalf("merchant-%02d was lost to a concurrent remove", i)
		}
	}
	for i := 1; i < writers; i += 2 {
		if _, ok := index[fmt.Sprintf("doomed-%02d", i)]; ok {
			t.Fatalf("doomed-%02d survived its remove, resurrected by a concurrent add", i)
		}
	}
}

func setFor(i int) Set {
	return Set{
		Login:     fmt.Sprintf("login-%02d", i),
		TransKey:  fmt.Sprintf("trans-%02d", i),
		SecretKey: fmt.Sprintf("secret-%02d", i),
	}
}
