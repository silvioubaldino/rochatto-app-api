package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/silvioubaldino/sales-backend/internal/auth"
	"github.com/silvioubaldino/sales-backend/internal/handlers"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	_ = godotenv.Load()

	env := getenv("ENV", "development")
	port := getenv("PORT", "8080")
	databaseURL := os.Getenv("DATABASE_URL")
	firebaseProjectID := os.Getenv("FIREBASE_PROJECT_ID")
	frontendURL := getenv("FRONTEND_URL", "*")

	if databaseURL == "" {
		log.Fatal("DATABASE_URL não configurada")
	}
	if firebaseProjectID == "" {
		log.Fatal("FIREBASE_PROJECT_ID não configurada")
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("erro ao conectar ao banco: %v", err)
	}

	apiMux := http.NewServeMux()
	handlers.NewClientesHandler(db).Register(apiMux)
	handlers.NewProdutosHandler(db).Register(apiMux)
	handlers.NewFornecedoresHandler(db).Register(apiMux)
	handlers.NewVendedoresExternosHandler(db).Register(apiMux)
	handlers.NewVendasHandler(db).Register(apiMux)
	handlers.NewQuotesHandler(db).Register(apiMux)
	handlers.NewPagamentosHandler(db).Register(apiMux)
	handlers.NewEstoqueHandler(db).Register(apiMux)
	handlers.NewDashboardHandler(db).Register(apiMux)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", handlers.Health(env))
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", auth.RequireAuth(firebaseProjectID)(apiMux)))

	handler := auth.CORS(frontendURL)(root)

	log.Printf("servidor iniciado na porta %s (env=%s)", port, env)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("erro ao iniciar servidor: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
