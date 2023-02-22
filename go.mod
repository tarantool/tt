module github.com/tarantool/tt

go 1.17

require (
	github.com/adam-hanna/arrayOperations v0.2.6
	github.com/alecthomas/participle/v2 v2.0.0-alpha4
	github.com/apex/log v1.9.0
	github.com/briandowns/spinner v1.11.1
	github.com/c-bata/go-prompt v0.2.6
	github.com/dave/jennifer v1.5.0
	github.com/docker/docker v20.10.7+incompatible
	github.com/fatih/color v1.13.0
	github.com/hashicorp/go-version v1.4.0
	github.com/magefile/mage v1.12.1
	github.com/mattn/go-isatty v0.0.14
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d
	github.com/mitchellh/mapstructure v1.4.3
	github.com/moby/term v0.0.0-20221105221325-4eb28fa6025c
	github.com/otiai10/copy v1.7.1
	github.com/spf13/cobra v1.3.0
	github.com/stretchr/testify v1.7.1
	github.com/tarantool/cartridge-cli v0.0.0-20220605082730-53e6a5be9a61
	github.com/tarantool/go-tarantool v1.9.0
	github.com/vmihailenco/msgpack/v5 v5.3.5
	github.com/yuin/gopher-lua v0.0.0-20220504180219-658193537a64
	golang.org/x/crypto v0.0.0-20220315160706-3147a52a75dd
	golang.org/x/term v0.5.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/FZambia/tarantool v0.2.1 // indirect
	github.com/Microsoft/go-winio v0.5.2 // indirect
	github.com/Microsoft/hcsshim v0.9.6 // indirect
	github.com/StackExchange/wmi v0.0.0-20210224194228-fe8f1750fd46 // indirect
	github.com/avast/retry-go v3.0.0+incompatible // indirect
	github.com/containerd/cgroups v1.0.4 // indirect
	github.com/containerd/containerd v1.6.18 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.1+incompatible // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/go-ole/go-ole v1.2.5 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/hpcloud/tail v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.9 // indirect
	github.com/mattn/go-tty v0.0.3 // indirect
	github.com/moby/sys/mount v0.3.3 // indirect
	github.com/moby/sys/mountinfo v0.6.2 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.3-0.20211202183452-c5a74bcca799 // indirect
	github.com/opencontainers/runc v1.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/term v1.2.0-beta.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/robfig/config v0.0.0-20141207224736-0f78529c8c7e // indirect
	github.com/shirou/gopsutil v3.21.2+incompatible // indirect
	github.com/shurcooL/httpfs v0.0.0-20190707220628-8d4bc4ba7749 // indirect
	github.com/sirupsen/logrus v1.8.1 // indirect
	github.com/spacemonkeygo/spacelog v0.0.0-20180420211403-2296661a0572 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tarantool/go-openssl v0.0.8-0.20220711094538-d93c1eff4f49 // indirect
	github.com/tklauser/go-sysconf v0.3.4 // indirect
	github.com/tklauser/numcpus v0.2.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opencensus.io v0.23.0 // indirect
	golang.org/x/net v0.0.0-20220722155237-a158d28d115b // indirect
	golang.org/x/sys v0.5.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20220502173005-c8bf987b8c21 // indirect
	google.golang.org/grpc v1.47.0 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/fsnotify.v1 v1.4.7 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/vmihailenco/msgpack.v2 v2.9.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/c-bata/go-prompt => github.com/tarantool/go-prompt v0.2.6-tarantool
	github.com/tarantool/cartridge-cli => ./cli/cartridge/third_party/cartridge-cli
	golang.org/x/crypto => golang.org/x/crypto v0.0.0-20211202192323-5770296d904e
	golang.org/x/net => golang.org/x/net v0.7.0
	golang.org/x/sys => golang.org/x/sys v0.0.0-20210817190340-bfb29a6856f2
)
