// Package version holds build metadata.
package version

// Version is the release version, overridable with -ldflags.
var Version = "1.0.0"

// Commit and Date are set at build time when available.
var Commit = "dev"
var Date = "unknown"
