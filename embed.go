package main

import "embed"

// index.html is baked into the binary at build time.
// In development, handleStatic checks for a file on disk first, so
// editing index.html still takes effect without a rebuild.
//
//go:embed index.html
var embeddedFS embed.FS
