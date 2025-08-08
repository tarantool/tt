# Known issues

The file includes built-in errors we expect people to encounter and common
problems reported by users. For the latest list of all issues see
the [Github Issues page](https://github.com/tarantool/tt/issues).

## Contents

- [Do not set the box.cfg.background](#do-not-set-the-boxcfgbackground)

## Do not set the box.cfg.background

`tt start` daemonize a Tarantool process itself. The [box.cfg.background
setting][cfg-background] does the same thing from a Tarantool process. These
features conflict with each other. As a result, `tt status` shows an invalid
status of a Tarantool instance, and it is unable to stop the instance
with `tt stop`.

You should never set `box.cfg.background = true` in an application code that
is started by `tt`.

[cfg-background]: https://www.tarantool.io/en/doc/latest/reference/configuration/#confval-background
