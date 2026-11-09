// Package bundle is the contract of a source of prebuilt archives: where
// tt binary install and tt binary search find the builds of a product and
// where they download them from.
//
// A source answers two questions - which builds of a product there are for
// a platform ([Source.List]) and what the bytes of one of them are
// ([Source.Fetch]) - and keeps the rest to itself: how it logs in, how its
// archives are named, where they are stored. tt chooses the build, checks
// it, unpacks it and installs it the same way whatever the source.
//
// The sources of tt are the choice of its distribution, made once, before
// any command is built: tt itself has the customer zone of tarantool.io, and
// a distribution may replace it or have none (see core.WithBundleSources).
// --local-dir, a directory of archives, is not a source a distribution
// chooses: tt always has it.
package bundle

import (
	"context"
	"io"

	"github.com/spf13/pflag"
)

// Channel is how mature a build is.
type Channel string

const (
	// ChannelRelease is the released builds, the ones tt installs by default.
	ChannelRelease Channel = "release"
	// ChannelDev is the development builds, which --dev asks for.
	ChannelDev Channel = "dev"
)

// Query asks a source for the builds of one product for one platform.
type Query struct {
	// Product is the product as tt binary names it: "tarantool-ee",
	// "tarantool-sdk", "tcm", "tt-ee". The archives of tarantool-ee are the
	// SDK ones; a source that has them answers both.
	Product string
	// OS and Arch are the platform the build must run on, as runtime.GOOS
	// and runtime.GOARCH name it: "linux", "darwin"; "amd64", "arm64".
	OS, Arch string
	// Channel is the maturity of the builds asked for.
	Channel Channel
	// Version narrows the query to a release line: "3", "3.4", or a whole
	// version, as tt binary search --version names it. Empty asks for every
	// version. A source that cannot narrow a query answers it whole.
	Version string
}

// Artifact is one archive of a build.
type Artifact struct {
	// Version is the version of the product, without the flavours of the
	// build: "3.4.1-0-r63".
	Version string
	// Flavor lists the variants of the build in the order the product
	// writes them in front of the version: "debug", "gc64", "nogc64". tt
	// identifies a build by both: debug-gc64-3.4.1-0-r63. tt installs a
	// debug build only on request and takes gc64 for an SDK by default.
	Flavor []string
	// Name is the file name of the archive.
	Name string
	// Size is the size of the archive in bytes, 0 if the source does not
	// know it.
	Size int64
	// SHA256 is the hex SHA-256 digest of the archive, empty if the source
	// does not know it. tt checks the bytes Fetch writes against it.
	SHA256 string
	// Locator is the source's own reference to the archive, opaque to tt:
	// an artifact goes back to Fetch as List returned it.
	Locator string
}

// Source supplies prebuilt archives of products.
//
// tt calls a source from one goroutine at a time, and only from commands:
// a source may keep state between the calls of a command, such as a
// session it logged in with.
type Source interface {
	// Name names the source in messages and logs: "customer-zone".
	Name() string
	// Products lists the products the source has builds of, as Query names
	// them.
	Products() []string
	// List returns the builds of q, in no particular order. A product with
	// no build for the platform or the channel is an empty result, not an
	// error. An error means the source could not answer: tt reports it and
	// tries no other source.
	List(ctx context.Context, q Query) ([]Artifact, error)
	// Fetch writes the bytes of an artifact List returned to dst. A failure
	// may leave a part of them written. A source with a digest of the
	// archive of its own checks it.
	Fetch(ctx context.Context, artifact Artifact, dst io.Writer) error
}

// FlagSource is a Source with options of its own, such as the credentials
// it logs in with. tt adds them to each command that searches or installs
// one of the products of the source. A flag the command already has is not
// added again: the flags of one source share their destinations between
// the commands.
type FlagSource interface {
	Source
	// RegisterFlags adds the flags of the source for product to flags.
	RegisterFlags(product string, flags *pflag.FlagSet)
}
