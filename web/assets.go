package webassets

import "embed"

// FS contains the intentionally tiny, old-web-facing interface.
//
//go:embed static/* templates/* reference/*
var FS embed.FS
