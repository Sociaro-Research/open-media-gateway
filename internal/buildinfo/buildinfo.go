// Package buildinfo reports the Go build identity of a command binary.
package buildinfo

import "runtime/debug"

// version is the release tag set by image builds through
// -ldflags "-X github.com/Sociaro-Research/open-media-gateway/internal/buildinfo.version=vX.Y.Z".
var version string

// Identity returns "<name> <version>" plus VCS metadata when the build recorded
// it. The version is the linked release tag, else the module version; builds
// with neither (Docker builds exclude .git) report "(devel)".
func Identity(name string) string {
	v := version
	var revision string
	var modified bool
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" {
			v = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if v == "" {
		v = "(devel)"
	}
	identity := name + " " + v
	if revision != "" {
		identity += " revision=" + revision
	}
	if modified {
		identity += " modified=true"
	}
	return identity
}
