package main

import "runtime/debug"

// Version is the build version. For release builds GoReleaser sets it via
// -ldflags "-X main.Version=...". For `go install ...@version` builds, where
// no ldflags are passed, it falls back to the module version embedded by the
// toolchain. An explicit ldflags value always wins when present.
var Version = "devel"

func init() {
	if Version != "devel" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			Version = v
		}
	}
}
