module github.com/Liapoldus/core

go 1.26.0

require gopkg.in/yaml.v3 v3.0.1

require liapoldus.local/plugin-sdk v0.0.0

replace liapoldus.local/plugin-sdk => ../plugin-sdk

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	golang.org/x/crypto v0.57.0
	modernc.org/sqlite v1.59.0
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/exp/typeparams v0.0.0-20250911091902-df9299821621 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	honnef.co/go/tools v0.7.0 // indirect
	modernc.org/libc v1.75.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

tool honnef.co/go/tools/cmd/staticcheck
