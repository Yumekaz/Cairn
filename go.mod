module github.com/yumekaz/cairn

go 1.26.4

require (
	github.com/go-chi/chi/v5 v5.3.0
	github.com/google/uuid v1.6.0
	github.com/spf13/cobra v1.10.2
	github.com/yumekaz/duraflow v0.0.0-20260625093653-bb71a0f07d94
	golang.org/x/sys v0.42.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.52.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lib/pq v1.12.3 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// Local sibling checkout (clone DURAFLOW next to Cairn). Override for other layouts:
//   go mod edit -replace=github.com/yumekaz/duraflow=/absolute/path/to/DURAFLOW
replace github.com/yumekaz/duraflow => ../DURAFLOW
