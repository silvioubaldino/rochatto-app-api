package oauth

import (
	"database/sql/driver"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type StringArray []string

var pgMap = pgtype.NewMap()

func (a *StringArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	return pgMap.SQLScanner((*[]string)(a)).Scan(src)
}

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

func (StringArray) GormDataType() string { return "text[]" }
