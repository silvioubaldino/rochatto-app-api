// Comando migrate: aplica/inspeciona as migrations embutidas no binário.
//
//	go run ./cmd/migrate up         # aplica todas as pendentes
//	go run ./cmd/migrate down 1     # desfaz N migrations (N obrigatório, ≥ 1)
//	go run ./cmd/migrate version    # versão atual (+ dirty)
//	go run ./cmd/migrate force N    # marca a versão N sem rodar SQL (baseline / sair de dirty)
//
// Lê DATABASE_URL do ambiente (ou do .env).
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/silvioubaldino/sales-backend/internal/dbmigrate"
)

const usage = "uso: migrate up | down N | version | force N"

func main() {
	_ = godotenv.Load()
	log.SetFlags(0)

	if len(os.Args) < 2 {
		log.Fatal(usage)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL não configurada")
	}

	m, err := dbmigrate.New(databaseURL)
	if err != nil {
		log.Fatalf("erro ao preparar migrations: %v", err)
	}
	defer m.Close()

	if err := run(m, os.Args[1:]); err != nil {
		m.Close()
		log.Fatalf("erro: %v", err)
	}
}

func run(m *dbmigrate.Migrator, args []string) error {
	switch args[0] {
	case "up":
		if err := m.Up(); err != nil {
			return err
		}
	case "down":
		n, err := intArg(args)
		if err != nil {
			return err
		}
		if n < 1 {
			return fmt.Errorf("down exige N ≥ 1")
		}
		if err := m.M.Steps(-n); err != nil {
			return err
		}
	case "force":
		n, err := intArg(args)
		if err != nil {
			return err
		}
		if err := m.M.Force(n); err != nil {
			return err
		}
	case "version":
		// só imprime abaixo
	default:
		return fmt.Errorf("comando desconhecido %q — %s", args[0], usage)
	}
	v, dirty, err := m.Version()
	if err != nil {
		return err
	}
	fmt.Printf("version=%d dirty=%t\n", v, dirty)
	return nil
}

func intArg(args []string) (int, error) {
	if len(args) < 2 {
		return 0, fmt.Errorf("%s exige um número — %s", args[0], usage)
	}
	n, err := strconv.Atoi(args[1])
	if err != nil {
		return 0, fmt.Errorf("número inválido %q", args[1])
	}
	return n, nil
}
