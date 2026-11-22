package toolkit

import (
	"context"
	"net/url"
)

const (
	CodeInvalidInput  = "INVALID_INPUT"
	CodeNotFound      = "NOT_FOUND"
	CodeConflict      = "CONFLICT"
	CodeUnprocessable = "UNPROCESSABLE"
	CodeForbidden     = "FORBIDDEN"
	CodeRateLimited   = "RATE_LIMITED"
	CodeInternal      = "INTERNAL"
)

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

func NewToolError(code, msg string) *ToolError { return &ToolError{Code: code, Message: msg} }

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Status 5xx e inesperados viram INTERNAL — o pipeline substitui a mensagem (detalhe interno nunca sai).
func (e *APIError) ToToolError() *ToolError {
	switch e.Status {
	case 400:
		return NewToolError(CodeInvalidInput, e.Message)
	case 404:
		return NewToolError(CodeNotFound, e.Message)
	case 409:
		return NewToolError(CodeConflict, e.Message)
	case 422:
		return NewToolError(CodeUnprocessable, e.Message)
	default:
		return NewToolError(CodeInternal, e.Message)
	}
}

type Gateway interface {
	Do(ctx context.Context, method, path string, query url.Values, body, out any) error
}
