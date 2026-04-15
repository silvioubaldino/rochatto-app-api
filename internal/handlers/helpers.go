package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/models"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func validateNome(w http.ResponseWriter, nome string) (string, bool) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		models.WriteError(w, http.StatusBadRequest, "campo 'nome' é obrigatório")
		return "", false
	}
	if len(nome) > 200 {
		models.WriteError(w, http.StatusBadRequest, "campo 'nome' deve ter no máximo 200 caracteres")
		return "", false
	}
	return nome, true
}
