package tools

import "time"

// Tipos compartilhados de saída (AYD-006 §4.2) e seus decodificadores do formato REST.
// Campos opcionais usam ponteiro + omitempty (omitidos, nunca null).

import "github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"

type CustomerSummary struct {
	ID           UUID                `json:"id"`
	Name         string              `json:"name"`
	Phone        *string             `json:"phone,omitempty"`
	Email        *string             `json:"email,omitempty"`
	City         *string             `json:"city,omitempty"`
	Address      *string             `json:"address,omitempty"`
	LeadSource   *toolkit.LeadSource `json:"lead_source,omitempty"`
	ReferrerID   *UUID               `json:"referrer_id,omitempty"`
	ReferrerName *string             `json:"referrer_name,omitempty"`
	Notes        *string             `json:"notes,omitempty"`
}

type ProductSummary struct {
	ID             UUID     `json:"id"`
	Name           string   `json:"name"`
	Description    *string  `json:"description,omitempty"`
	CategoryID     *UUID    `json:"category_id,omitempty"`
	CategoryName   *string  `json:"category_name,omitempty"`
	Unit           *string  `json:"unit,omitempty"`
	ReferencePrice *float64 `json:"reference_price,omitempty"`
	ReferenceCost  *float64 `json:"reference_cost,omitempty"`
}

type SupplierSummary struct {
	ID    UUID    `json:"id"`
	Name  string  `json:"name"`
	Phone *string `json:"phone,omitempty"`
	City  *string `json:"city,omitempty"`
	Notes *string `json:"notes,omitempty"`
}

type PartnerSummary struct {
	ID    UUID                `json:"id"`
	Name  string              `json:"name"`
	Type  toolkit.PartnerType `json:"type"`
	Phone *string             `json:"phone,omitempty"`
	Notes *string             `json:"notes,omitempty"`
}

type QuoteItem struct {
	ID          UUID    `json:"id"`
	ProductID   *UUID   `json:"product_id,omitempty"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	UnitCost    float64 `json:"unit_cost"`
	TotalPrice  float64 `json:"total_price"`
}

// QuoteSummary é o QuoteDetail sem Items (listagens).
type QuoteSummary struct {
	ID           UUID                `json:"id"`
	Status       toolkit.QuoteStatus `json:"status"`
	CustomerID   UUID                `json:"customer_id"`
	CustomerName string              `json:"customer_name"`
	ReferrerID   *UUID               `json:"referrer_id,omitempty"`
	ReferrerName *string             `json:"referrer_name,omitempty"`
	QuoteDate    Date                `json:"quote_date"`
	Notes        *string             `json:"notes,omitempty"`
	LostReason   *string             `json:"lost_reason,omitempty"`
	QuickSale    bool                `json:"quick_sale"`
	TotalQuote   float64             `json:"total_quote"`
}

type QuoteDetail struct {
	QuoteSummary
	Items []QuoteItem `json:"items"`
}

type SaleSummary struct {
	ID                   UUID                   `json:"id"`
	QuoteID              *UUID                  `json:"quote_id,omitempty"`
	CustomerID           UUID                   `json:"customer_id"`
	CustomerName         string                 `json:"customer_name"`
	ReferrerID           *UUID                  `json:"referrer_id,omitempty"`
	ReferrerName         *string                `json:"referrer_name,omitempty"`
	SaleDate             Date                   `json:"sale_date"`
	ExpectedDeliveryDate *Date                  `json:"expected_delivery_date,omitempty"`
	DeliveryStatus       toolkit.DeliveryStatus `json:"delivery_status"`
	PaymentStatus        toolkit.PaymentStatus  `json:"payment_status"`
	Total                float64                `json:"total"`
	TotalCost            float64                `json:"total_cost"`
	TotalReceived        float64                `json:"total_received"`
	Receivable           float64                `json:"receivable"`
	Profit               float64                `json:"profit"`
	FreightAmount        float64                `json:"freight_amount"`
	FreightPaid          float64                `json:"freight_paid"`
	FreightPayable       float64                `json:"freight_payable"`
	CommissionAmount     float64                `json:"commission_amount"`
	CommissionPaid       bool                   `json:"commission_paid"`
	Notes                *string                `json:"notes,omitempty"`
}

type SaleItem struct {
	ID           UUID               `json:"id"`
	ProductID    *UUID              `json:"product_id,omitempty"`
	ProductName  string             `json:"product_name"`
	SupplierID   *UUID              `json:"supplier_id,omitempty"`
	SupplierName *string            `json:"supplier_name,omitempty"`
	Origin       toolkit.ItemOrigin `json:"origin"`
	PurchaseID   *UUID              `json:"purchase_id,omitempty"`
	Quantity     float64            `json:"quantity"`
	UnitPrice    float64            `json:"unit_price"`
	UnitCost     float64            `json:"unit_cost"`
}

type Receipt struct {
	ID            UUID                   `json:"id"`
	Amount        float64                `json:"amount"`
	Status        toolkit.ReceiptStatus  `json:"status"`
	ScheduledDate *Date                  `json:"scheduled_date,omitempty"`
	PaidAt        *Date                  `json:"paid_at,omitempty"`
	PaymentMethod *toolkit.PaymentMethod `json:"payment_method,omitempty"`
	Notes         *string                `json:"notes,omitempty"`
}

type SaleDetail struct {
	SaleSummary
	Items    []SaleItem `json:"items"`
	Receipts []Receipt  `json:"receipts"`
}

// PurchaseSummary é o PurchaseDetail do AYD-002 sem as linhas (listagens).
type PurchaseSummary struct {
	ID            UUID                          `json:"id"`
	Kind          toolkit.PurchaseKind          `json:"kind"`
	Status        toolkit.PurchaseStatus        `json:"status"`
	SupplierID    UUID                          `json:"supplier_id"`
	SupplierName  string                        `json:"supplier_name"`
	ExpectedDate  *Date                         `json:"expected_date,omitempty"`
	Notes         *string                       `json:"notes,omitempty"`
	OrderedAt     *Date                         `json:"ordered_at,omitempty"`
	ReceivedAt    *Date                         `json:"received_at,omitempty"`
	PaymentStatus toolkit.SupplierPaymentStatus `json:"payment_status"`
	PaidAmount    *float64                      `json:"paid_amount,omitempty"`
	PaidAt        *Date                         `json:"paid_at,omitempty"`
	TotalCost     float64                       `json:"total_cost"`
	ToPay         float64                       `json:"to_pay"`
	LineCount     int                           `json:"line_count"`
	CreatedAt     time.Time                     `json:"created_at"`
}

// PurchaseSaleItem é o item de venda atendido por uma Purchase, com os campos legados PT
// da rota REST traduzidos para EN (AYD-006 §4.2/§5, "Pending purchase item"). product_id
// não vem do REST (que não o expõe em sale_items) e fica ausente.
type PurchaseSaleItem struct {
	ID           UUID    `json:"id"`
	SaleID       UUID    `json:"sale_id"`
	CustomerName string  `json:"customer_name"`
	ProductID    *UUID   `json:"product_id,omitempty"`
	ProductName  string  `json:"product_name"`
	Quantity     float64 `json:"quantity"`
	UnitCost     float64 `json:"unit_cost"`
}

// restPurchaseSaleItem espelha o JSON legado PT de sale_items na rota REST.
type restPurchaseSaleItem struct {
	ID           UUID    `json:"id"`
	SaleID       UUID    `json:"sale_id"`
	CustomerName string  `json:"customer_name"`
	ProdutoID    *UUID   `json:"produto_id"`
	ProdutoNome  string  `json:"produto_nome"`
	Quantidade   float64 `json:"quantidade"`
	CustoUnit    float64 `json:"custo_unit"`
}

type restPurchaseDetail struct {
	PurchaseSummary
	SaleItems []restPurchaseSaleItem `json:"sale_items"`
	Items     []PurchaseLine         `json:"items"`
}

type PurchaseLine struct {
	ID          UUID    `json:"id"`
	ProductID   UUID    `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitCost    float64 `json:"unit_cost"`
}

type PurchaseDetail struct {
	PurchaseSummary
	SaleItems []PurchaseSaleItem `json:"sale_items"`
	Items     []PurchaseLine     `json:"items"`
}

// StockBalanceRow é a linha de GET /stock/balance (AYD-002, já EN).
type StockBalanceRow struct {
	ProductID      UUID     `json:"product_id"`
	ProductName    string   `json:"product_name"`
	Unit           *string  `json:"unit,omitempty"`
	InflowQty      float64  `json:"inflow_qty"`
	OutflowQty     float64  `json:"outflow_qty"`
	AdjustmentQty  float64  `json:"adjustment_qty"`
	Balance        float64  `json:"balance"`
	LastInflowCost *float64 `json:"last_inflow_cost,omitempty"`
	LastInflowDate *Date    `json:"last_inflow_date,omitempty"`
}

type StockMovement struct {
	Kind        toolkit.MovementKind `json:"kind"`
	Date        Date                 `json:"date"`
	Quantity    float64              `json:"quantity"`
	UnitCost    *float64             `json:"unit_cost,omitempty"`
	ReferenceID string               `json:"reference_id"`
	Description string               `json:"description"`
}

type StockBalanceDetail struct {
	StockBalanceRow
	Movements []StockMovement `json:"movements"`
}

// --- Formato REST legado (PT) lido pelos adaptadores ---

type legacyCustomer struct {
	ID           UUID                `json:"id"`
	Nome         string              `json:"nome"`
	Telefone     *string             `json:"telefone"`
	Email        *string             `json:"email"`
	Endereco     *string             `json:"endereco"`
	Cidade       *string             `json:"cidade"`
	Observacoes  *string             `json:"observacoes"`
	LeadSource   *toolkit.LeadSource `json:"lead_source"`
	ReferrerID   *UUID               `json:"referrer_id"`
	ReferrerName *string             `json:"referrer_name"`
}

func (c legacyCustomer) toEN() CustomerSummary {
	return CustomerSummary{
		ID: c.ID, Name: c.Nome, Phone: opt(c.Telefone), Email: opt(c.Email), City: opt(c.Cidade),
		Address: opt(c.Endereco), LeadSource: c.LeadSource, ReferrerID: optUUID(c.ReferrerID),
		ReferrerName: opt(c.ReferrerName), Notes: opt(c.Observacoes),
	}
}

type legacyProduct struct {
	ID              UUID     `json:"id"`
	Nome            string   `json:"nome"`
	Descricao       *string  `json:"descricao"`
	PrecoReferencia *float64 `json:"preco_referencia"`
	CustoReferencia *float64 `json:"custo_referencia"`
	Unidade         *string  `json:"unidade"`
	CategoryID      *UUID    `json:"category_id"`
	CategoryName    *string  `json:"category_name"`
}

func (p legacyProduct) toEN() ProductSummary {
	return ProductSummary{
		ID: p.ID, Name: p.Nome, Description: opt(p.Descricao), CategoryID: optUUID(p.CategoryID),
		CategoryName: opt(p.CategoryName), Unit: opt(p.Unidade), ReferencePrice: p.PrecoReferencia,
		ReferenceCost: p.CustoReferencia,
	}
}

type legacySupplier struct {
	ID          UUID    `json:"id"`
	Nome        string  `json:"nome"`
	Telefone    *string `json:"telefone"`
	Cidade      *string `json:"cidade"`
	Observacoes *string `json:"observacoes"`
}

func (s legacySupplier) toEN() SupplierSummary {
	return SupplierSummary{ID: s.ID, Name: s.Nome, Phone: opt(s.Telefone), City: opt(s.Cidade), Notes: opt(s.Observacoes)}
}

type legacySale struct {
	ID                  UUID                   `json:"id"`
	ClienteID           UUID                   `json:"cliente_id"`
	ClienteNome         string                 `json:"cliente_nome"`
	VendedorExternoID   *UUID                  `json:"vendedor_externo_id"`
	VendedorExternoNome *string                `json:"vendedor_externo_nome"`
	QuoteID             *UUID                  `json:"quote_id"`
	DeliveryStatus      toolkit.DeliveryStatus `json:"delivery_status"`
	PaymentStatus       toolkit.PaymentStatus  `json:"payment_status"`
	DataVenda           Date                   `json:"data_venda"`
	DataEntregaPrevista *Date                  `json:"data_entrega_prevista"`
	FreteValor          float64                `json:"frete_valor"`
	FretePago           float64                `json:"frete_pago"`
	ComissaoValor       float64                `json:"comissao_valor"`
	ComissaoPaga        bool                   `json:"comissao_paga"`
	Observacoes         *string                `json:"observacoes"`
	TotalVenda          float64                `json:"total_venda"`
	TotalCusto          float64                `json:"total_custo"`
	TotalRecebido       float64                `json:"total_recebido"`
	Receivable          float64                `json:"receivable"`
	FreightPayable      float64                `json:"freight_payable"`
	Lucro               float64                `json:"lucro"`
}

func (s legacySale) toEN() SaleSummary {
	return SaleSummary{
		ID: s.ID, QuoteID: optUUID(s.QuoteID), CustomerID: s.ClienteID, CustomerName: s.ClienteNome,
		ReferrerID: optUUID(s.VendedorExternoID), ReferrerName: opt(s.VendedorExternoNome),
		SaleDate: s.DataVenda, ExpectedDeliveryDate: optDate(s.DataEntregaPrevista),
		DeliveryStatus: s.DeliveryStatus, PaymentStatus: s.PaymentStatus,
		Total: s.TotalVenda, TotalCost: s.TotalCusto, TotalReceived: s.TotalRecebido,
		Receivable: s.Receivable, Profit: s.Lucro,
		FreightAmount: s.FreteValor, FreightPaid: s.FretePago, FreightPayable: s.FreightPayable,
		CommissionAmount: s.ComissaoValor, CommissionPaid: s.ComissaoPaga, Notes: opt(s.Observacoes),
	}
}

func optDate(d *Date) *Date {
	if d == nil || *d == "" {
		return nil
	}
	return d
}

type legacySaleItem struct {
	ID             UUID               `json:"id"`
	ProdutoID      *UUID              `json:"produto_id"`
	ProdutoNome    string             `json:"produto_nome"`
	FornecedorID   *UUID              `json:"fornecedor_id"`
	FornecedorNome *string            `json:"fornecedor_nome"`
	Origin         toolkit.ItemOrigin `json:"origin"`
	PurchaseID     *UUID              `json:"purchase_id"`
	Quantidade     float64            `json:"quantidade"`
	PrecoVendaUnit float64            `json:"preco_venda_unit"`
	CustoUnit      float64            `json:"custo_unit"`
}

func (i legacySaleItem) toEN() SaleItem {
	return SaleItem{
		ID: i.ID, ProductID: optUUID(i.ProdutoID), ProductName: i.ProdutoNome,
		SupplierID: optUUID(i.FornecedorID), SupplierName: opt(i.FornecedorNome), Origin: i.Origin,
		PurchaseID: optUUID(i.PurchaseID), Quantity: i.Quantidade, UnitPrice: i.PrecoVendaUnit, UnitCost: i.CustoUnit,
	}
}

type legacyReceipt struct {
	ID            UUID                   `json:"id"`
	Valor         float64                `json:"valor"`
	DataPagamento *Date                  `json:"data_pagamento"`
	DataAgendada  *Date                  `json:"data_agendada"`
	Status        string                 `json:"status"`
	PaymentMethod *toolkit.PaymentMethod `json:"payment_method"`
	Observacoes   *string                `json:"observacoes"`
}

func (r legacyReceipt) toEN() Receipt {
	status := toolkit.ReceiptStatus("PENDING")
	if r.Status == "recebido" {
		status = "RECEIVED"
	}
	return Receipt{
		ID: r.ID, Amount: r.Valor, Status: status, ScheduledDate: optDate(r.DataAgendada),
		PaidAt: optDate(r.DataPagamento), PaymentMethod: r.PaymentMethod, Notes: opt(r.Observacoes),
	}
}

type legacySaleDetail struct {
	legacySale
	Itens      []legacySaleItem `json:"itens"`
	Pagamentos []legacyReceipt  `json:"pagamentos"`
}

func (d legacySaleDetail) toEN() SaleDetail {
	out := SaleDetail{SaleSummary: d.legacySale.toEN(), Items: make([]SaleItem, 0, len(d.Itens)), Receipts: make([]Receipt, 0, len(d.Pagamentos))}
	for _, i := range d.Itens {
		out.Items = append(out.Items, i.toEN())
	}
	for _, p := range d.Pagamentos {
		out.Receipts = append(out.Receipts, p.toEN())
	}
	return out
}
