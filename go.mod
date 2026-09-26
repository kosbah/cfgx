module github.com/example/cfgx

go 1.21

require (
	github.com/fsnotify/fsnotify v1.7.0
	github.com/spf13/cobra v1.8.1
	github.com/spf13/viper v1.19.0
)

// NOTE: The following are indirect dependencies brought in by cobra/viper.
// Run `go mod tidy` after initial implementation to populate the full require block and go.sum.
