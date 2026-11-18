package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
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

// normalizeName aplica trim e valida um nome obrigatório (até 200 caracteres) cujo campo
// no payload é `field` (contratos em inglês — AYD-003). Devolve o nome normalizado e a
// mensagem de erro ("" quando válido).
func normalizeName(field, name string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "campo '" + field + "' é obrigatório"
	}
	if len([]rune(name)) > 200 {
		return "", "campo '" + field + "' deve ter no máximo 200 caracteres"
	}
	return name, ""
}

// nullIfBlank aplica trim e converte texto vazio em null.
func nullIfBlank(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// isUniqueViolation indica violação de unicidade no Postgres (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
