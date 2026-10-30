// Package integrityprovider holds the integrity provider tt runs with: the
// one the distribution of tt chose, or the disabled provider, which checks
// nothing, when it chose none.
//
// tt registers its integrity flags, sets up the checks and builds the
// signers and verifiers of a configuration storage through the provider
// Get returns, never through the package-level functions of
// github.com/tarantool/tt/sdk/integrity, which are the disabled provider.
// The provider is set once, when tt boots and before any command is built,
// by the core: a module has no way to change it.
package integrityprovider

import (
	"sync/atomic"

	"github.com/tarantool/tt/sdk/integrity"
)

// holder boxes a provider: atomic.Pointer needs one concrete type, and the
// providers are of different types.
type holder struct {
	provider integrity.Provider
}

// active is the provider of the process; nil until Set.
var active atomic.Pointer[holder]

// Set makes provider the provider of the process. A nil provider is the
// disabled one.
func Set(provider integrity.Provider) {
	if provider == nil {
		provider = integrity.NewDisabledProvider()
	}

	active.Store(&holder{provider: provider})
}

// Get returns the provider of the process: the one Set was given last, and
// the disabled provider before Set.
func Get() integrity.Provider {
	current := active.Load()
	if current == nil {
		return integrity.NewDisabledProvider()
	}

	return current.provider
}
