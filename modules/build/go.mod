module github.com/tarantool/tt/modules/build

go 1.27.0

require (
	github.com/stretchr/testify v1.11.1
	github.com/tarantool/tt/sdk/build/v3 v3.0.0
	github.com/tarantool/tt/sdk/v3 v3.0.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.8 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/tarantool/go-config/v2 v2.0.2-0.20261116202956-580e0b6621b4 // indirect
	github.com/tarantool/tt/sdk/connect/v3 v3.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/tarantool/tt/sdk/build/v3 => ../../sdk/build
	github.com/tarantool/tt/sdk/connect/v3 => ../../sdk/connect
	github.com/tarantool/tt/sdk/v3 => ../../sdk
)
