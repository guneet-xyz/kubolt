package version

// Version is set at build time via -ldflags:
//
//	go build -ldflags "-X github.com/guneet-xyz/kubolt/internal/version.Version=v1.0.0"
//
// Defaults to "dev" for local development builds.
var Version = "dev"
