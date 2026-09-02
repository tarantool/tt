# Migration from older versions

This file contains information on how to migrate existing projects from
one `tt` version to a newer one. Additionally to migration hints, it
contains breaking changes descriptions.

## Contents

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0

### Migrate applications from instances.enabled

`tt` 3.0 uses the directory containing `tt.yaml` as the only application root.
It no longer reads `env.instances_enabled` or scans an `instances.enabled`
directory. A legacy environment can therefore no longer manage several
applications through symlinks such as these:

```text
<environment>/
├── tt.yaml
├── instances.available/
│   ├── app1/
│   │   ├── init.lua
│   │   └── instances.yml
│   └── app2/
│       ├── init.lua
│       └── instances.yml
└── instances.enabled/
    ├── app1 -> ../instances.available/app1
    └── app2 -> ../instances.available/app2
```

Migrate every application while still using `tt` 2.x:

1. Stop the application and resolve its `instances.enabled` symlink. Use the
   real target directory as the application root, or copy it to a new
   standalone location. Do not copy the symlink itself.
2. Put a separate `tt.yaml` into the application root. If it is copied from the
   shared environment, check all relative paths because they are now resolved
   from the new configuration location.
3. Set `env.instances_enabled` to `.` in the application configuration:

   ```yaml
   env:
     instances_enabled: .
   ```

   This is a compatibility step that lets `tt` 2.x manage the application
   directly from its future root.
4. Run `tt` with that application's configuration and verify that lifecycle
   commands work without going through the symlink. For example:

   ```console
   tt --cfg /srv/apps/app1/tt.yaml status app1
   ```

The final layout has one configuration and no registry symlink per
application:

```text
/srv/apps/app1/
├── tt.yaml
├── init.lua
├── instances.yml
└── var/
```

Repeat the procedure for every application before upgrading to `tt` 3.0.
After the upgrade, remove `instances_enabled` from each `tt.yaml`: the option no
longer exists. Run `tt` from the application root or pass its configuration via
`--cfg`. Remove the old `instances.enabled` directory only after every
application has been verified.

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
