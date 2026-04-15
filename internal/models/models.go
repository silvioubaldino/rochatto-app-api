package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base com UUID gerado pelo banco via trigger — GORM não gera o UUID.
type Base struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type Cliente struct {
	Base
	Nome        string  `gorm:"not null"         json:"nome"`
	Telefone    *string `                        json:"telefone"`
	Email       *string `                        json:"email"`
	Endereco    *string `                        json:"endereco"`
	Cidade      *string `                        json:"cidade"`
	Observacoes *string `                        json:"observacoes"`
}

func (Cliente) TableName() string { return "clientes" }

type Produto struct {
	Base
	Nome            string   `gorm:"not null" json:"nome"`
	Descricao       *string  `                json:"descricao"`
	PrecoReferencia *float64 `gorm:"type:decimal(12,2)" json:"preco_referencia"`
	CustoReferencia *float64 `gorm:"type:decimal(12,2)" json:"custo_referencia"`
	Unidade         *string  `                json:"unidade"`
}

func (Produto) TableName() string { return "produtos" }

type Fornecedor struct {
	Base
	Nome        string  `gorm:"not null" json:"nome"`
	Telefone    *string `               json:"telefone"`
	Cidade      *string `               json:"cidade"`
	Observacoes *string `               json:"observacoes"`
}

func (Fornecedor) TableName() string { return "fornecedores" }

type VendedorExterno struct {
	Base
	Nome        string  `gorm:"not null" json:"nome"`
	Telefone    *string `               json:"telefone"`
	Observacoes *string `               json:"observacoes"`
}

func (VendedorExterno) TableName() string { return "vendedores_externos" }

type Venda struct {
	Base
	ClienteID           uuid.UUID  `gorm:"type:uuid;not null"                             json:"cliente_id"`
	VendedorExternoID   *uuid.UUID `gorm:"type:uuid"                                      json:"vendedor_externo_id"`
	Status              string     `gorm:"type:status_venda;not null;default:'A_ENTREGAR'" json:"status"`
	DataVenda           time.Time  `gorm:"type:date;not null"                              json:"data_venda"`
	DataEntregaPrevista *time.Time `gorm:"type:date"                                       json:"data_entrega_prevista"`
	FreteValor          float64    `gorm:"type:decimal(12,2);not null;default:0"            json:"frete_valor"`
	FretePago           float64    `gorm:"type:decimal(12,2);not null;default:0"            json:"frete_pago"`
	ComissaoValor       float64    `gorm:"type:decimal(12,2);not null;default:0"            json:"comissao_valor"`
	ComissaoPaga        bool       `gorm:"not null;default:false"                           json:"comissao_paga"`
	Observacoes         *string    `                                                        json:"observacoes"`
}

func (Venda) TableName() string { return "vendas" }

type ItemVenda struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	VendaID        uuid.UUID  `gorm:"type:uuid;not null"                             json:"venda_id"`
	ProdutoID      *uuid.UUID `gorm:"type:uuid"                                      json:"produto_id"`
	ProdutoNome    string     `gorm:"not null"                                        json:"produto_nome"`
	FornecedorID   *uuid.UUID `gorm:"type:uuid"                                      json:"fornecedor_id"`
	FornecedorNome *string    `                                                        json:"fornecedor_nome"`
	Quantidade     float64    `gorm:"type:decimal(12,3);not null"                     json:"quantidade"`
	PrecoVendaUnit float64    `gorm:"type:decimal(12,2);not null"                     json:"preco_venda_unit"`
	CustoUnit      float64    `gorm:"type:decimal(12,2);not null"                     json:"custo_unit"`
	PagoFornecedor float64    `gorm:"type:decimal(12,2);not null;default:0"           json:"pago_fornecedor"`
	CreatedAt      time.Time  `                                                        json:"created_at"`
}

func (ItemVenda) TableName() string { return "itens_venda" }

type PagamentoCliente struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	VendaID       uuid.UUID  `gorm:"type:uuid;not null"                             json:"venda_id"`
	Valor         float64    `gorm:"type:decimal(12,2);not null"                     json:"valor"`
	DataPagamento *time.Time `gorm:"type:date"                                       json:"data_pagamento"`
	DataAgendada  *time.Time `gorm:"type:date"                                       json:"data_agendada"`
	Status        string     `gorm:"type:status_pagamento;not null;default:'pendente'" json:"status"`
	Observacoes   *string    `                                                        json:"observacoes"`
	CreatedAt     time.Time  `                                                        json:"created_at"`
}

func (PagamentoCliente) TableName() string { return "pagamentos_cliente" }

type Notificacao struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Tipo           string    `gorm:"not null"           json:"tipo"`
	VendaID        *uuid.UUID `gorm:"type:uuid"         json:"venda_id"`
	PagamentoID    *uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"pagamento_id"`
	ClienteNome    string    `gorm:"not null"           json:"cliente_nome"`
	Valor          float64   `gorm:"type:decimal(12,2);not null" json:"valor"`
	DataRef        time.Time `gorm:"type:date;not null" json:"data_ref"`
	DiasRestantes  *int      `                          json:"dias_restantes"`
	Lida           bool      `gorm:"not null;default:false" json:"lida"`
	CreatedAt      time.Time `                          json:"created_at"`
}

func (Notificacao) TableName() string { return "notificacoes" }

type Estoque struct {
	ID                uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ProdutoID         *uuid.UUID `gorm:"type:uuid"                                      json:"produto_id"`
	ProdutoNome       string     `gorm:"not null"                                        json:"produto_nome"`
	FornecedorID      *uuid.UUID `gorm:"type:uuid"                                      json:"fornecedor_id"`
	QuantidadeEntrada float64    `gorm:"type:decimal(12,3);not null"                     json:"quantidade_entrada"`
	CustoUnit         float64    `gorm:"type:decimal(12,2);not null"                     json:"custo_unit"`
	DataEntrada       time.Time  `gorm:"type:date;not null"                              json:"data_entrada"`
	Observacoes       *string    `                                                        json:"observacoes"`
	CreatedAt         time.Time  `                                                        json:"created_at"`
}

func (Estoque) TableName() string { return "estoque" }
