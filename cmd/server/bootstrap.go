package main

import (
	"log"
	"os"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/auth"
	"github.com/silvioubaldino/sales-backend/internal/dbmigrate"
)

func ownerAllowlist(env string) auth.Allowlist {
	allow, err := auth.LoadAllowlist(env, os.Getenv("ALLOWED_EMAILS"))
	if err != nil {
		log.Fatal(err)
	}
	if allow.Empty() {
		log.Printf("WARN ALLOWED_EMAILS vazia: qualquer conta Firebase válida acessa a API (aceitável só em %s)", env)
	} else {
		log.Printf("acesso restrito a %d e-mail(s) (ALLOWED_EMAILS)", allow.Len())
	}
	return allow
}

func migrateOnStart(databaseURL string) {
	if !strings.EqualFold(os.Getenv("MIGRATE_ON_START"), "true") {
		return
	}
	v, err := dbmigrate.Up(databaseURL)
	if err != nil {
		log.Fatalf("erro ao aplicar migrations: %v", err)
	}
	log.Printf("migrations aplicadas (version=%d)", v)
}
