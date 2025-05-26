# Docker file used in workflows:
# full-ci-ce-linux-arm64, create-packages-linux-arm64, tests-ce-linux-arm64
FROM ubuntu:20.04

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

RUN apt update \
    && apt -y upgrade \
    && apt install --no-install-suggests -y \
        vim \
        git \
        cpio \
        wget \
        curl \
        rsync \
        golang \
        make \
        sudo \
        gcc \
        cmake \
        unzip \
        zip \
        build-essential \
        ninja-build \
        lua5.1 \
        luarocks \
        lcov \
        ruby-dev \
        liblz4-dev \
        autoconf \
        automake \
        libtool \
        libssl-dev \
        python3-pytest \
        python3-psutil \
        python3-pip \
        lsb-release \
        software-properties-common \
    && apt clean

RUN add-apt-repository ppa:longsleep/golang-backports \
    && apt update \
    && apt install -y golang \
    && apt clean

RUN if [ "$(uname -m)" = "x86_64" ]; \
        then ARCHITECTURE="64bit"; \
    elif [ "$(uname -m)" = "aarch64" ]; \
        then ARCHITECTURE="ARM64"; \
    else \
        echo "Unsupported platform!"; exit 0; \
    fi \
    && wget -P /tmp/ "https://github.com/magefile/mage/releases/download/v1.15.0/mage_1.15.0_Linux-${ARCHITECTURE}.tar.gz" \
    && tar xzf "/tmp/mage_1.15.0_Linux-${ARCHITECTURE}.tar.gz" -C /usr/local/bin/ \
    && rm -rf /tmp/*

RUN echo 'deb [trusted=yes] https://repo.goreleaser.com/apt/ /' > /etc/apt/sources.list.d/goreleaser.list \
    && apt update \
    && apt install -y goreleaser=1.12.3 \
    && apt clean

RUN wget -P /tmp/ https://github.com/openssl/openssl/releases/download/openssl-3.0.8/openssl-3.0.8.tar.gz \
    && tar -xvf /tmp/openssl-3.0.8.tar.gz -C /tmp/ \
    && cd /tmp/openssl-3.0.8 \
    && ./Configure --prefix=/opt/openssl no-shared \
    && make -j \
    && make install \
    && rm -rf /tmp/*

RUN ETCD_VER=v3.5.9 \
    FILENAME="etcd-${ETCD_VER}-linux-$(uname -m | awk '{print ($0 == "x86_64")?"amd64":"arm64"}')" \
    && mkdir -p /opt/etcd/ \
    && curl -L "https://github.com/etcd-io/etcd/releases/download/${ETCD_VER}/${FILENAME}.tar.gz" -o "/opt/etcd/${FILENAME}.tar.gz" \
    && tar xvzf "/opt/etcd/${FILENAME}.tar.gz" -C /opt/etcd --strip-components=1 \
    && rm /opt/etcd/*.tar.gz

RUN curl -L https://tarantool.io/iqJapRm/release/3/installer.sh | bash \
    && apt install -y tarantool tarantool-dev \
    && apt clean
