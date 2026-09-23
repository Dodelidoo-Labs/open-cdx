package accounts

import (
	"errors"
	"strconv"
	"strings"
)

var errCatalogVersionUnknown = errors.New("catalog refresh requires a known Codex client version")

// Accounts share one native catalog across devices. Never let an older device
// or a refresh without client information downgrade its discovery version.
func catalogClientVersion(stored, requested string) string {
	current, currentOK := parseCatalogVersion(stored)
	next, nextOK := parseCatalogVersion(requested)
	if !currentOK {
		if nextOK {
			return requested
		}
		return ""
	}
	if nextOK {
		for index := range current {
			if next[index] > current[index] {
				return requested
			}
			if next[index] < current[index] {
				break
			}
		}
	}
	return stored
}

func parseCatalogVersion(value string) ([3]uint64, bool) {
	var version [3]uint64
	parts := strings.Split(value, ".")
	if len(parts) != len(version) {
		return version, false
	}
	for index, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version, false
		}
		version[index] = number
	}
	return version, version != [3]uint64{}
}
