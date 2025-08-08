# Migration from older versions

This file contains information on how to migrate existing projects from
one `tt` version to a newer one. Additionally to migration hints, it
contains breaking changes descriptions.

## Contents

- [1.3.1 -> 2.0.0](#131---200)

## 1.3.1 -> 2.0.0

### New format of tt config file

`tt` 2.0.0 configuration file format is incompatible with previous version:

- Root `tt` section is removed.
- Common environment configuration is moved to the `env` section.
All relative paths in this section are relative to the config file location.
- Relative path in `app` section are relative to the application directory.

### New runtime artifacts layout

Runtime artifacts layout is changed:

- Relative paths are relative to the application directory. In case of
single script instance, a directory is created in the instances
enabled directory.
- Since relative paths already contains an application name,
only instance name is appended to the result directory. Here is an
example of 2.0.0 default layout for a local environment:

```text
instances.enabled/app/
├── init.lua
├── instances.yml
└── var
    ├── lib
    │   ├── inst1
    │   └── inst2
    ├── log
    │   ├── inst1
    │   └── inst2
    └── run
        ├── inst1
        └── inst2
```

Moving artifacts from 1.* versions:

- Create artifacts directories in application dir: var/lib, var/log, var/run.
- Copy instance sub-directories from 1.* environment to application
dir. Data artifacts copying example:
`cp -r <env_dir>/var/lib/app/* <instances_enabled>/app/var/lib/`

Absolute paths are not affected by these layout changes, because an application
name is always appended for them.

### Working directory is changed

Instance process working directory is changed to the application directory.
It was `tt` current working directory previously. So, if the instance
code work with files using relative paths, these files must be moved/copied
to the application directory.
