// Package dbmigrate aplica as migrations embutidas (package migrations) com golang-migrate.
package dbmigrate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/silvioubaldino/sales-backend/migrations"
)

type Migrator struct {
	M  *migrate.Migrate
	db *sql.DB
}

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

func (m *Migrator) Close() error {
	srcErr, dbErr := m.M.Close()
	return errors.Join(srcErr, dbErr, m.db.Close())
}

func (m *Migrator) Up() error {
	if err := m.M.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func (m *Migrator) Version() (uint, bool, error) {
	v, dirty, err := m.M.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

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
