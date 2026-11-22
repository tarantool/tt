module github.com/tarantool/tt/sdk/v3

go 1.27.0

require (
	github.com/jedib0t/go-pretty/v6 v6.7.8
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	github.com/tarantool/go-config/v2 v2.0.2-0.20261116202956-580e0b6621b4
	github.com/tarantool/go-prompt v1.0.2-0.20261118001635-bc2cfa6de863
	github.com/tarantool/tt/sdk/build/v3 v3.0.0-rc.1
	github.com/tarantool/tt/sdk/connect/v3 v3.0.0-rc.1
	go.yaml.in/yaml/v2 v2.4.3
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/term v0.46.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.8 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mattn/go-tty v0.0.7 // indirect
	github.com/pkg/term v1.2.0-beta.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tarantool/go-option v1.1.0 // indirect
	github.com/tarantool/go-storage/v2 v2.0.1-0.20261030080914-3d28af2e7c8c // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/tarantool/tt/sdk/build/v3 => ./build
	github.com/tarantool/tt/sdk/connect/v3 => ./connect
)
