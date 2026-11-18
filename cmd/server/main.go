package main

import (
	"log"
	"net/http"
	"os"
	_ "time/tzdata" // fuso America/Sao_Paulo mesmo em imagem sem tzdata

	"github.com/joho/godotenv"
	"github.com/silvioubaldino/sales-backend/internal/auth"
	"github.com/silvioubaldino/sales-backend/internal/handlers"
	"github.com/silvioubaldino/sales-backend/internal/mcp"
	"github.com/silvioubaldino/sales-backend/internal/mcp/gateway"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
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

	allow := ownerAllowlist(env)
	// Assistant connector (AYD-006): MCP_ENABLED=false (padrão) não monta nada; com true,
	// PUBLIC_BASE_URL/WEB_APP_URL inválidas impedem a api de subir.
	mcpCfg, err := oauth.LoadConfig(env, os.Getenv)
	if err != nil {
		log.Fatalf("configuração do Assistant connector: %v", err)
	}
	migrateOnStart(databaseURL)

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("erro ao conectar ao banco: %v", err)
	}

	apiMux := http.NewServeMux()
	handlers.NewClientesHandler(db).Register(apiMux)
	handlers.NewProdutosHandler(db).Register(apiMux)
	handlers.NewFornecedoresHandler(db).Register(apiMux)
	handlers.NewPartnersHandler(db).Register(apiMux)
	handlers.NewProductCategoriesHandler(db).Register(apiMux)
	handlers.NewVendasHandler(db).Register(apiMux)
	handlers.NewQuotesHandler(db).Register(apiMux)
	handlers.NewPagamentosHandler(db).Register(apiMux)
	handlers.NewPurchasesHandler(db).Register(apiMux)
	handlers.NewStockHandler(db).Register(apiMux)
	handlers.NewDashboardHandler(db).Register(apiMux)
	handlers.NewFinanceHandler(db).Register(apiMux)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", handlers.Health(env))
	if mcpCfg.Enabled {
		oauthServer := oauth.New(db, mcpCfg, allow)
		oauthServer.RegisterAPI(apiMux)  // consentimento e Connections (atrás do Firebase + allowlist)
		oauthServer.RegisterPublic(root) // discovery e /oauth/*
		mcpHandler, err := mcp.NewHandler(mcp.Options{
			Config:   mcpCfg,
			Verifier: oauthServer,
			Pipeline: mcp.PipelineOptions{Gateway: gateway.New(apiMux), Audit: mcp.NewDBAuditor(db), Deduper: mcp.NewDBDeduper(db, nil)},
		})
		if err != nil {
			log.Fatalf("Assistant connector: %v", err)
		}
		root.Handle("/mcp", mcpHandler)
		log.Printf("Assistant connector habilitado em %s (catalog %s)", mcpCfg.Resource(), mcp.CatalogVersion)
	}
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", auth.RequireAuth(firebaseProjectID, allow)(apiMux)))

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
