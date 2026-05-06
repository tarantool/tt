package cluster

import (
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

type factoryOptions struct {
	fileReadFunc FileReadFunc
	integrity    *IntegrityOptions
}

// FactoryOption configures collector and publisher factories.
type FactoryOption func(*factoryOptions)

// WithIntegrity enables typed storage integrity settings for storage-backed
// collectors and publishers.
func WithIntegrity(opts IntegrityOptions) FactoryOption {
	optsCopy := IntegrityOptions{
		Hashers:         append([]hasher.Hasher(nil), opts.Hashers...),
		Signers:         append([]gcrypto.Signer(nil), opts.Signers...),
		Verifiers:       append([]gcrypto.Verifier(nil), opts.Verifiers...),
		SignerVerifiers: append([]gcrypto.SignerVerifier(nil), opts.SignerVerifiers...),
	}

	return func(dst *factoryOptions) {
		dst.integrity = &optsCopy
	}
}

// WithFileReadFunc overrides file reading for file-backed collectors.
func WithFileReadFunc(fileReadFunc FileReadFunc) FactoryOption {
	return func(dst *factoryOptions) {
		dst.fileReadFunc = fileReadFunc
	}
}

func applyFactoryOptions(opts []FactoryOption) factoryOptions {
	var out factoryOptions

	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}

	return out
}
