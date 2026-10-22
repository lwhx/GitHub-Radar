package migrations

import "embed"

// Files contains all versioned SQLite migrations.
//
//go:embed *.sql
var Files embed.FS
