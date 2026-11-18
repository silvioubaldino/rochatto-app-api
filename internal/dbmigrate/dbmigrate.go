// Package dbmigrate aplica as migrations embutidas (package migrations) com golang-migrate.
//
// Usa o driver pgx/v5 a partir de um *sql.DB, então DATABASE_URL pode estar em qualquer
// formato aceito pelo pgx (URL postgres:// / postgresql:// ou DSN key=value).
package dbmigrate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver database/sql "pgx"

	"github.com/silvioubaldino/sales-backend/migrations"
)

// Migrator encapsula o *migrate.Migrate e a conexão que ele usa.
type Migrator struct {
	M  *migrate.Migrate
	db *sql.DB
}

// New abre uma conexão dedicada e prepara o golang-migrate com as migrations embutidas.
// Chame Close ao terminar.
func New(databaseURL string) (*Migrator, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("source iofs: %w", err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("driver pgx: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Migrator{M: m, db: db}, nil
}

// Close libera source, driver e conexão.
func (m *Migrator) Close() error {
	srcErr, dbErr := m.M.Close()
	return errors.Join(srcErr, dbErr, m.db.Close())
}

// Up aplica todas as migrations pendentes. "Nada a aplicar" não é erro.
func (m *Migrator) Up() error {
	if err := m.M.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// Version devolve a versão atual (0 se nenhuma migration foi aplicada) e o flag dirty.
func (m *Migrator) Version() (uint, bool, error) {
	v, dirty, err := m.M.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

// Up é o atalho usado no startup do servidor (MIGRATE_ON_START=true).
func Up(databaseURL string) (version uint, err error) {
	m, err := New(databaseURL)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, m.Close()) }()
	if err := m.Up(); err != nil {
		return 0, err
	}
	v, _, err := m.Version()
	return v, err
}
