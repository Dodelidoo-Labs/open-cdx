package httpapi

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestCatalogBuildFailuresLogOncePerChangeAndOnRecovery(t *testing.T) {
	var lines []string
	server := &Server{logf: func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }}
	for _, err := range []error{nil, errors.New("bad shape"), errors.New("bad shape"), errors.New("other"), nil, nil} {
		server.logCatalogBuild(err)
	}
	want := []string{
		"device catalog build failed: bad shape",
		"device catalog build failed: other",
		"device catalog build recovered",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines=%q", lines)
	}
}
