# Known issues

The file includes built-in errors we expect people to encounter and common
problems reported by users. For the latest list of all issues see
the [Github Issues page](https://github.com/tarantool/tt/issues).

## Contents

- [A project of tt 2 is not moved in place](#a-project-of-tt-2-is-not-moved-in-place)
- [Do not set the box.cfg.background](#do-not-set-the-boxcfgbackground)

## Do not set the box.cfg.background

`tt instance start` daemonize a Tarantool process itself. The
[box.cfg.background setting][cfg-background] does the same thing from a
Tarantool process. These features conflict with each other. As a result,
`tt instance status` shows an invalid status of a Tarantool instance, and it
is unable to stop the instance with `tt instance stop`.

You should never set `box.cfg.background = true` in an application code that
is started by `tt`.

[cfg-background]: https://www.tarantool.io/en/doc/latest/reference/configuration/#confval-background

## A project of tt 2 is not moved in place

tt 3.0.0 is for new installations. Moving an existing project of tt 2 to the
layout of tt 3 in place is not supported, and a project that is partly moved
loses data.

The data of an instance of tt 2 lives in `<project>/var/lib/<instance>`, and
`tt instance start` refuses the instances of a project whose `<project>/var/lib`
exists while `<project>/.tarantool/var/lib` does not: the instance would start
on an empty database next to its data. Once one instance of the project has
started and created `<project>/.tarantool/var/lib`, the check passes, and the
instances that still keep their data in `<project>/var/lib/<instance>` start
on an empty database: `tt instance start` does not look into
`<project>/var/lib` any more.

Build a project on tt 3 anew in a [project](concepts/projects-and-scopes.md),
and take the data of every instance over deliberately, with a copy rather
than by starting the instance in place.
