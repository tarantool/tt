FROM ubuntu:22.04

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

# setup Python
RUN apt update \
    && apt install -y software-properties-common wget curl tar \
    && add-apt-repository ppa:deadsnakes/ppa -y \
    && apt update 

RUN apt install -y python3.10 python3.10-dev python3.10-distutils python3.10-venv
RUN wget https://bootstrap.pypa.io/get-pip.py && python3.10 get-pip.py

# setup Go
ENV GO_VERSION=1.25.7
ENV ETCD_VERSION=v3.5.9 
ENV GOLANGCI_LINT_VERSION=v1.63.4
ENV GO_OS=linux
ENV GO_ARCH=amd64

RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.${GO_OS}-${GO_ARCH}.tar.gz -o go.tar.gz && \
    tar -C /usr/local -xzf go.tar.gz && \
    rm go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
# setup test environment
RUN curl -L https://my.tech.vk.com/download/tarantool/3/installer.sh | bash \
    && apt-get -y install tarantool tarantool-dev

RUN wget https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}/etcd-${ETCD_VERSION}-linux-amd64.tar.gz \
    && tar xzvf etcd-${ETCD_VERSION}-linux-amd64.tar.gz \
    && mv etcd-${ETCD_VERSION}-linux-amd64/etcd* /usr/local/bin/
# install tt build requirements
RUN  apt -y update \
    && apt -y install git gcc make cmake unzip zip gdb libssl-dev \
    && apt-get --allow-releaseinfo-change update \
    && apt-get -y -f install \
        build-essential ninja-build \
        lua5.1 luarocks lcov \
        ruby-dev liblz4-dev  autoconf \
        automake libtool zsh fish \
    && luarocks install luacheck 0.26.1 \
    && gem install coveralls-lcov \
    && pip3 install tarantool 

COPY ./ /tt

WORKDIR /tt
# setup test environment
RUN go install github.com/magefile/mage@latest \
    && go install github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}
ENV PATH="${PATH}:/root/go/bin:/go/bin:/tt"
RUN git submodule update --init --recursive
RUN mage build
RUN pip3 install -r test/requirements.txt
# Stop Mono server
RUN systemctl kill mono-xsp4 || true
