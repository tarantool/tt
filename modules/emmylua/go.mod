module github.com/tarantool/tt/modules/emmylua

go 1.27.0

require (
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
	github.com/tarantool/go-luarocks v0.1.0
	github.com/tarantool/tt/sdk v0.0.0
	github.com/tarantool/tt/sdk/fetch v0.0.0
)

require (
	dario.cat/mergo v1.0.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.0 // indirect
	github.com/go-git/go-git/v5 v5.19.2 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.8 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/pjbgf/sha1cd v0.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/tarantool/go-config/v2 v2.0.2-0.20261102222747-4c7c90ea0ec7 // indirect
	github.com/tarantool/go-option v1.1.0 // indirect
	github.com/tarantool/go-storage/v2 v2.0.1-0.20261030080914-3d28af2e7c8c // indirect
	github.com/tarantool/tt/sdk/connect v0.0.0-0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/term v0.47.0 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/tarantool/tt/sdk => ../../sdk
	github.com/tarantool/tt/sdk/connect => ../../sdk/connect
	github.com/tarantool/tt/sdk/fetch => ../../sdk/fetch
)
