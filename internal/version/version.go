// Package version carries build metadata injected with -ldflags -X.
package version

var (
	// Version is the release tag, e.g. "v0.3.1". "dev" for local builds.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = ""
	// Repo is the GitHub "owner/name" used for update checks.
	Repo = "phpandrew/TSG-Embroidery-Pro"
)

const AppName = "TSG Embroidery Pro"
