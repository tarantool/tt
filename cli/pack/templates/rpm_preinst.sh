SYSUSER=tarantool

if ! id "$SYSUSER" >/dev/null 2>&1; then
    /usr/sbin/groupadd -r $SYSUSER >/dev/null 2>&1 || :

    /usr/sbin/useradd -M -N -g $SYSUSER -r -d /var/lib/tarantool -s /sbin/nologin \
    -c "Tarantool Server" $SYSUSER >/dev/null 2>&1 || :
fi
