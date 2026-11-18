// Package testdb cria um banco Postgres descartável, com todas as migrations aplicadas,
// para testes de integração. Roda só com TEST_DATABASE_URL e permissão de CREATE DATABASE;
// senão o teste é pulado. Cada teste tem o seu banco — não há interferência entre pacotes
// que rodam em paralelo.
package testdb

import (
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/silvioubaldino/sales-backend/internal/dbmigrate"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var seq atomic.Int64

// New devolve um *gorm.DB sobre um banco novo (migrations 001…N aplicadas).
func New(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), seq.Add(1))
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Skipf("sem permissão para criar banco descartável: %v", err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	connStr := stdlib.RegisterConnConfig(cfg)

	m, err := dbmigrate.New(connStr)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	m.Close()

	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
		stdlib.UnregisterConnConfig(connStr)
		admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		admin.Close()
	})
	return db
}
