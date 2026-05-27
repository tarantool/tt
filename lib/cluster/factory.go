package cluster

import (
	"fmt"
	"time"

	"github.com/tarantool/go-storage"
	gcrypto "github.com/tarantool/go-storage/crypto"
	"github.com/tarantool/go-storage/hasher"
)

// IntegrityOptions configure integrity-aware typed storage builder options.
type IntegrityOptions struct {
	Hashers         []hasher.Hasher
	Signers         []gcrypto.Signer
	Verifiers       []gcrypto.Verifier
	SignerVerifiers []gcrypto.SignerVerifier
}

// Factory creates collectors and publishers for cluster configuration data.
// A zero Factory is usable and behaves like a plain (no-integrity) factory.
type Factory struct {
	fileReadFunc FileReadFunc
	integrity    *IntegrityOptions
}

// Option configures a Factory.
type Option func(*Factory)

// WithIntegrity enables typed storage integrity settings for storage-backed
// collectors and publishers. Defensive copies of the slices are taken.
func WithIntegrity(opts IntegrityOptions) Option {
	optsCopy := IntegrityOptions{
		Hashers:         append([]hasher.Hasher(nil), opts.Hashers...),
		Signers:         append([]gcrypto.Signer(nil), opts.Signers...),
		Verifiers:       append([]gcrypto.Verifier(nil), opts.Verifiers...),
		SignerVerifiers: append([]gcrypto.SignerVerifier(nil), opts.SignerVerifiers...),
	}
	return func(f *Factory) {
		f.integrity = &optsCopy
	}
}

// WithFileReadFunc overrides file reading for file-backed collectors.
func WithFileReadFunc(fileReadFunc FileReadFunc) Option {
	return func(f *Factory) {
		f.fileReadFunc = fileReadFunc
	}
}

// NewFactory creates a new Factory configured by the given options.
func NewFactory(opts ...Option) Factory {
	var f Factory
	for _, opt := range opts {
		if opt != nil {
			opt(&f)
		}
	}
	return f
}

// NewFileCollector creates a file-based DataCollector. If the factory was
// configured with WithFileReadFunc, that function is used instead of os.Open.
func (f Factory) NewFileCollector(path string) DataCollector {
	return newFileCollector(path, f.fileReadFunc)
}

// NewFilePublisher creates a file-based DataPublisher. It is unsupported when
// the factory has integrity options configured (file storage cannot carry the
// auxiliary data needed for signatures).
func (f Factory) NewFilePublisher(path string) (DataPublisher, error) {
	if f.integrity != nil {
		return nil, fmt.Errorf("publishing into a file with integrity data is not supported")
	}
	return NewFilePublisher(path), nil
}

// NewRemoteStorage creates a *RawStorage bound to the given storage backend.
// The returned value implements both DataCollector and DataPublisher.
func (f Factory) NewRemoteStorage(stor storage.Storage,
	prefix, key string, timeout time.Duration, storageType string,
) (*RawStorage, error) {
	return NewStorage(stor, prefix, timeout, key, storageType, f.integrity, configLocation)
}
