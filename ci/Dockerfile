# The image mage docker runs the tests in: what the CI jobs of
# .github/actions/prepare-ce-test-env install, for amd64 and arm64. It has no
# Tarantool: TT_TEST_TARANTOOL picks the one the tests run against, and tt
# installs it into the test cache. mage docker builds the image with the Go
# version of go.mod and test/requirements.txt as its context.
ARG GO_VERSION=1.27.3

FROM golang:${GO_VERSION} AS go-toolchain

FROM ubuntu:24.04

COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/opt/venv/bin:/usr/local/go/bin:${PATH}"

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

# The tools tt and its tests run, the shells of the completion tests and what
# tt binary install --source-build needs to build Tarantool. The GitHub
# runners have two of them CI does not install: libpq, with their
# PostgreSQL, for the pg rock, and zstd, which tar runs for the backup
# archives of the tests.
RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        autoconf \
        automake \
        bash-completion \
        build-essential \
        ca-certificates \
        cmake \
        cpio \
        curl \
        fish \
        gdb \
        git \
        libcurl4-openssl-dev \
        libicu-dev \
        liblz4-dev \
        libncurses-dev \
        libpq-dev \
        libreadline-dev \
        libssl-dev \
        libtool \
        libunwind-dev \
        libyaml-dev \
        lsb-release \
        make \
        ninja-build \
        pkg-config \
        python3-dev \
        python3-pip \
        python3-venv \
        rsync \
        unzip \
        zip \
        zlib1g-dev \
        zsh \
        zstd \
    && rm -rf /var/lib/apt/lists/*

# The architecture as each release names it.
RUN case "$(uname -m)" in \
        x86_64) echo "amd64 64bit x86_64" ;; \
        aarch64) echo "arm64 ARM64 aarch64" ;; \
        *) echo "Unsupported platform: $(uname -m)" >&2; exit 1 ;; \
    esac > /etc/tt-test-arch

RUN read -r _ mage _ < /etc/tt-test-arch \
    && curl -fsSL "https://github.com/magefile/mage/releases/download/v1.15.0/mage_1.15.0_Linux-${mage}.tar.gz" \
        | tar xz -C /usr/local/bin mage

RUN read -r arch _ < /etc/tt-test-arch \
    && ETCD_VER=v3.5.9 \
    && curl -fsSL "https://github.com/etcd-io/etcd/releases/download/${ETCD_VER}/etcd-${ETCD_VER}-linux-${arch}.tar.gz" \
        | tar xz -C /usr/local/bin --strip-components=1 \
            "etcd-${ETCD_VER}-linux-${arch}/etcd" "etcd-${ETCD_VER}-linux-${arch}/etcdctl"

# The docker client of the docker tests; mage docker passes the socket of the
# daemon of the host.
RUN read -r _ _ docker < /etc/tt-test-arch \
    && curl -fsSL "https://download.docker.com/linux/static/stable/${docker}/docker-29.1.3.tgz" \
        | tar xz -C /usr/local/bin --strip-components=1 docker/docker

# The golangci-lint version magefile.go pins.
RUN read -r arch _ < /etc/tt-test-arch \
    && LINT_VER=2.14.0 \
    && curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/v${LINT_VER}/golangci-lint-${LINT_VER}-linux-${arch}.tar.gz" \
        | tar xz -C /usr/local/bin --strip-components=1 \
            "golangci-lint-${LINT_VER}-linux-${arch}/golangci-lint"

COPY requirements.txt /tmp/requirements.txt
RUN python3 -m venv /opt/venv \
    && pip install --no-cache-dir -r /tmp/requirements.txt \
    && rm /tmp/requirements.txt

# The image has no Tarantool of its own: unless told otherwise, the tests run
# against the latest Tarantool CE ci/tarantool-versions.env names.
ENV TT_TEST_TARANTOOL=latest
