package models

import (
	"encoding/json"
	"net/http"
)

type APIError struct {
	Error string `json:"error"`
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{Error: msg})
}
