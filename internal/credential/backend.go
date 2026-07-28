package credential

type keychainBackend interface {
	Store(name, blob string) error
	Get(name string) (string, error)
	Delete(name string) error
}

var keychain keychainBackend = newCredsKeychain()

func setKeychainBackendForTest(backend keychainBackend) func() {
	previous := keychain
	keychain = backend
	return func() { keychain = previous }
}
