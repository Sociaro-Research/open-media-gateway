// Package buildinfo reports the Go build identity of a command binary.
package buildinfo

import "runtime/debug"

// Identity returns "<name> <module version>" plus VCS metadata when the build
// recorded it; unversioned builds report "(devel)".
func Identity(name string) string {
	version := "(devel)"
	var revision string
	var modified bool
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			version = info.Main.Version
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
	identity := name + " " + version
	if revision != "" {
		identity += " revision=" + revision
	}
	if modified {
		identity += " modified=true"
	}
	return identity
}
