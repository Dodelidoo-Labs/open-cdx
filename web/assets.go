package web

import "embed"

// Templates contains the server-rendered administration UI.
//
//go:embed templates/*.html
var Templates embed.FS

// Static contains the dashboard's CSP-compatible client-side behavior.
//
//go:embed static/*
var Static embed.FS
