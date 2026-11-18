package oauth

import (
	"database/sql/driver"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// StringArray mapeia TEXT[] do Postgres. Como driver.Valuer, o GORM não expande o slice
// em lista de parâmetros; na leitura usa o decodificador do pgx.
type StringArray []string

var pgMap = pgtype.NewMap()

// Scan implementa sql.Scanner (NULL → nil).
func (a *StringArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	return pgMap.SQLScanner((*[]string)(a)).Scan(src)
}

// Value implementa driver.Valuer, devolvendo o literal de array do Postgres.
func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// GormDataType diz ao GORM que o campo é um tipo de coluna próprio (TEXT[]).
func (StringArray) GormDataType() string { return "text[]" }
