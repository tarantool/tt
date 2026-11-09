// Package customerzone is the source of the archives of Tarantool Enterprise,
// the SDK, TCM and tt-ee: the customer zone of tarantool.io ([Source]) and a
// directory of archives downloaded from it ([Dir]).
//
// The customer zone lists the archives of a product with a request to its
// API that carries the login and the password, and answers with a session
// the downloads go with. The names of the archives carry the version and the
// flavours of a build; both sources read them the same way.
package customerzone

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/pflag"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// credentialsFlag names the file with the login and the password.
const credentialsFlag = "credentials-file"

// Source is the customer zone of tarantool.io.
type Source struct {
	doer            binary.TntIoDoer
	credentialsFile string
}

var _ bundle.FlagSource = (*Source)(nil)

// New returns the customer zone. It logs in with the credentials of
// --credentials-file, or of TT_CLI_EE_USERNAME and TT_CLI_EE_PASSWORD, or
// those the user types, at the first List.
func New() *Source {
	return newSource(binary.NewTntIoClient(""))
}

func newSource(doer binary.TntIoDoer) *Source {
	return &Source{doer: doer, credentialsFile: ""}
}

// Name implements bundle.Source.
func (*Source) Name() string {
	return "customer-zone"
}

// Products implements bundle.Source.
func (*Source) Products() []string {
	return productNames()
}

// RegisterFlags implements bundle.FlagSource.
func (s *Source) RegisterFlags(_ string, flags *pflag.FlagSet) {
	flags.StringVar(&s.credentialsFile, credentialsFlag, "",
		"File containing customer-zone login and password on separate lines")
}

// List implements bundle.Source.
func (s *Source) List(ctx context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	program, err := parseProduct(query.Product)
	if err != nil {
		return nil, err
	}

	// The platform is checked before the credentials are asked for.
	suffix, err := platformSuffix(program, query.OS, query.Arch)
	if err != nil {
		return nil, err
	}

	_, err = packageQuery(program, query, "")
	if err != nil {
		return nil, err
	}

	credentials, err := connect.GetCreds(s.credentialsFile)
	if err != nil {
		return nil, err
	}

	releases, err := packageVersions(ctx, credentials, program, query, s.doer)
	if err != nil {
		return nil, err
	}

	var artifacts []bundle.Artifact

	for release, names := range releases {
		directory, err := packageQuery(program, query, release)
		if err != nil {
			return nil, err
		}

		for _, name := range names {
			if !strings.HasSuffix(name, suffix) {
				continue
			}

			artifact, ok, err := parseArchive(program, name)
			if err != nil {
				log.Debugf("failed to parse the version of %s from package %s: %v",
					program, name, err)

				continue
			}

			if !ok {
				continue
			}

			artifact.Locator = PkgURI + "/" + directory + "/" + name
			artifacts = append(artifacts, artifact)
		}
	}

	return artifacts, nil
}

// Fetch implements bundle.Source. It goes with the session List logged in with.
func (s *Source) Fetch(ctx context.Context, artifact bundle.Artifact, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.Locator, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("User-Agent", "tt")
	binary.AddSessionCookie(req, s.doer.Token())

	return s.doer.Download(req, dst)
}
