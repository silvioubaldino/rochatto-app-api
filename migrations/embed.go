// Package migrations embute os arquivos SQL versionados (golang-migrate) no binário.
// Só entram arquivos no formato NNN_nome.up.sql / NNN_nome.down.sql.
package migrations

import "embed"

//go:embed *.up.sql *.down.sql
var FS embed.FS
