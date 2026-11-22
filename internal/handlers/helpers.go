package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, msg string) error { return &httpError{status, msg} }

func writeTxError(w http.ResponseWriter, op string, err error) {
	var he *httpError
	if errors.As(err, &he) {
		models.WriteError(w, he.status, he.msg)
		return
	}
	log.Printf("ERROR %s: %v", op, err)
	models.WriteError(w, http.StatusInternalServerError, "erro interno")
}

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

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func todayDate() time.Time {
	t := today()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type optionalUUID struct {
	Set   bool
	Value *uuid.UUID
}

func (o *optionalUUID) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var id uuid.UUID
	if err := json.Unmarshal(b, &id); err != nil {
		return err
	}
	o.Value = &id
	return nil
}
