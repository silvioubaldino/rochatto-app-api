package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

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

// httpError carrega o status HTTP de uma falha de regra de negócio dentro de uma transação
// (409/422/404/400); qualquer outro erro vira 500 em writeTxError.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, msg string) error { return &httpError{status, msg} }

// writeTxError responde com o status de um httpError ou loga e devolve 500.
func writeTxError(w http.ResponseWriter, op string, err error) {
	var he *httpError
	if errors.As(err, &he) {
		models.WriteError(w, he.status, he.msg)
		return
	}
	log.Printf("ERROR %s: %v", op, err)
	models.WriteError(w, http.StatusInternalServerError, "erro interno")
}

// parseOptionalDate converte "YYYY-MM-DD" (nil/vazio → nil). field nomeia o campo no erro.
func parseOptionalDate(field string, s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := parseDate(*s)
	if err != nil {
		return nil, errStatus(http.StatusBadRequest, field+" inválida (formato: YYYY-MM-DD)")
	}
	return &t, nil
}

// formatDate formata uma data opcional como "YYYY-MM-DD".
func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// todayDate devolve a data de hoje (fuso do negócio) como meia-noite UTC — para colunas DATE.
func todayDate() time.Time {
	t := today()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
