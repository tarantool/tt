# A host with systemd as its init, and the system user tarantool, as the deb
# and rpm packages of tt create it (package/scripts/preinstall.sh). Run it
# with --privileged, so that systemd manages the cgroups of its units.
FROM ubuntu
RUN apt-get update \
    && apt-get install -y --no-install-recommends systemd systemd-sysv \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -r tarantool \
    && useradd -M -N -g tarantool -r -d /var/lib/tarantool -s /sbin/nologin \
        -c "Tarantool Server" tarantool
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
