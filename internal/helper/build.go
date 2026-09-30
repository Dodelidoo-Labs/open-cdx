package helper

import "github.com/Dodelidoo-Labs/open-cdx/internal/version"

// BuildIdentity lets the menu app recognize a daemon left running by an
// earlier app version. It matches the text after "router-helper " in
// `router-helper version`.
func BuildIdentity() string {
	return version.Version + " (" + version.Commit + ")"
}
