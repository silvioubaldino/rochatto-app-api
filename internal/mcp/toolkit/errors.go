package toolkit

import (
	"context"
	"net/url"
)

// Códigos de erro de ferramenta (AYD-006 §3.5).
const (
	CodeInvalidInput  = "INVALID_INPUT"
	CodeNotFound      = "NOT_FOUND"
	CodeConflict      = "CONFLICT"
	CodeUnprocessable = "UNPROCESSABLE"
	CodeForbidden     = "FORBIDDEN"
	CodeRateLimited   = "RATE_LIMITED"
	CodeInternal      = "INTERNAL"
)

// ToolError é uma falha de regra ou de entrada devolvida como resultado isError.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// NewToolError cria um ToolError.
func NewToolError(code, msg string) *ToolError { return &ToolError{Code: code, Message: msg} }

// APIError é a resposta não-2xx de uma rota REST despachada pelo Gateway. Message é a
// mensagem pt-BR literal da API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// ToToolError mapeia o status HTTP para o código de ferramenta (§3.5). Status 5xx e
// inesperados viram INTERNAL — o pipeline substitui a mensagem (detalhe interno nunca sai).
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

// Gateway despacha, em processo, para as rotas REST /api/v1 (mesmos handlers, validações
// e transações). Ferramentas NÃO acessam o banco: só falam com o Gateway.
//
// path é a rota sem o prefixo /api/v1 (ex.: "/vendas/{id}"). body (nil = sem corpo) é
// serializado em JSON; out (nil = ignorar) recebe o JSON da resposta 2xx. Respostas
// não-2xx voltam como *APIError.
type Gateway interface {
	Do(ctx context.Context, method, path string, query url.Values, body, out any) error
}
