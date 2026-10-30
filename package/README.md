# Release packages

GoReleaser v2.19.0 builds two variants. Both install the `tt` executable.

| Variant | Targets | Configuration |
| --- | --- | --- |
| `tt` | Linux amd64/arm64, macOS arm64 | `goreleaser/.goreleaser.yml` |
| `tt-openssl` | Linux amd64/arm64, each on a native runner | `goreleaser/.goreleaser_openssl.yml` |

`tt` uses `CGO_ENABLED=0` and the `netgo,osusergo` tags. `tt-openssl` adds
`openssl,openssl_static`, enables cgo and links statically against OpenSSL.
The packaging workflow checks Linux binaries for an ELF interpreter and
shared-library dependencies before uploading them.

Each Linux target produces a deb and an rpm. A complete build therefore
produces eight packages, five binary archives and five binaries. `tt` and
`tt-openssl` declare mutual conflicts and replacements; `tt-openssl` also
provides `tt`. They share `/usr/bin/tt` and cannot be installed together.

The packages contain the executable and Bash, Zsh and Fish completions.
Their preinstall script creates the `tarantool` system account. They do not
install the old `/etc/tarantool/tt.yaml` or `/var/lib/tarantool/sys_env`.

To build a local snapshot, generate the completions first:

```sh
CGO_ENABLED=0 mage build
mkdir -p dist/completions
./tt completion bash > dist/completions/tt.bash
./tt completion zsh > dist/completions/tt.zsh
./tt completion fish > dist/completions/tt.fish
goreleaser release --snapshot --clean --config goreleaser/.goreleaser.yml
GOARCH=$(go env GOHOSTARCH) goreleaser release --snapshot --clean \
    --config goreleaser/.goreleaser_openssl.yml
```

The OpenSSL build requires a native Linux compiler and static OpenSSL
libraries (`build-essential`, `pkg-config` and `libssl-dev` on Ubuntu).
`GOARCH` selects the native architecture; this configuration does not
cross-compile OpenSSL.

The reusable `_packages.yml` workflow runs these configurations, stages
their artifacts with `ci/package_artifacts.py`, and invokes `smoke.sh` in
Ubuntu 24.04 and Rocky Linux 9 containers. Smoke checks install each amd64
package, exercise the CLI and completions, and check package conflicts.

Full CI downloads the snapshot binaries and passes their exact paths to
pytest. The EE TLS job uses the OpenSSL snapshot and the `tls` marker.
Package-related pull requests also run `_packages.yml` through
`packages.yml`. Tagged releases use the same builds and smoke checks,
then attach both variants to a draft GitHub release and upload all deb/rpm
files to RWS under the existing `tt` product.
