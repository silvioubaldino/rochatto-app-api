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
	ID    UUID                 `json:"id"`
	Name  string               `json:"name"`
	Kind  toolkit.SupplierKind `json:"kind"` // fornecedor ou prestador (AYD-007): STONE, TRANSPORT, LABOR, TAX_AUTHORITY, OTHER
	Phone *string              `json:"phone,omitempty"`
	City  *string              `json:"city,omitempty"`
	Notes *string              `json:"notes,omitempty"`
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
	PaidAmount    *float64                      `json:"paid_amount,omitempty"` // Σ quitado das contas da compra (AYD-007)
	PaidAt        *Date                         `json:"paid_at,omitempty"`
	TotalCost     float64                       `json:"total_cost"`
	ToPay         float64                       `json:"to_pay"` // Σ em aberto das contas da compra
	LineCount     int                           `json:"line_count"`
	CreatedAt     time.Time                     `json:"created_at"`
}

// restPurchaseSummary espelha o resumo da compra na rota REST (AYD-007): `paid` e `payable` (Σ das
// contas da compra) viram paid_amount e to_pay do contrato da ferramenta.
type restPurchaseSummary struct {
	ID            UUID                          `json:"id"`
	Kind          toolkit.PurchaseKind          `json:"kind"`
	Status        toolkit.PurchaseStatus        `json:"status"`
	SupplierID    UUID                          `json:"supplier_id"`
	SupplierName  string                        `json:"supplier_name"`
	ExpectedDate  *Date                         `json:"expected_date"`
	Notes         *string                       `json:"notes"`
	OrderedAt     *Date                         `json:"ordered_at"`
	ReceivedAt    *Date                         `json:"received_at"`
	PaymentStatus toolkit.SupplierPaymentStatus `json:"payment_status"`
	TotalCost     float64                       `json:"total_cost"`
	Paid          float64                       `json:"paid"`
	Payable       float64                       `json:"payable"`
	LineCount     int                           `json:"line_count"`
	CreatedAt     time.Time                     `json:"created_at"`
}

func (r restPurchaseSummary) toEN() PurchaseSummary {
	p := PurchaseSummary{
		ID: r.ID, Kind: r.Kind, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName,
		ExpectedDate: optDate(r.ExpectedDate), Notes: opt(r.Notes), OrderedAt: optDate(r.OrderedAt), ReceivedAt: optDate(r.ReceivedAt),
		PaymentStatus: r.PaymentStatus, TotalCost: r.TotalCost, ToPay: r.Payable, LineCount: r.LineCount, CreatedAt: r.CreatedAt,
	}
	if r.Paid > 0 {
		paid := r.Paid
		p.PaidAmount = &paid
	}
	return p
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
	restPurchaseSummary
	AllocationMethod toolkit.AllocationMethod `json:"allocation_method"`
	Composition      PurchaseComposition      `json:"composition"`
	Obligations      []ObligationSummary      `json:"obligations"`
	Open             float64                  `json:"open"`
	Lines            []PurchaseCostLine       `json:"lines"`
	SaleItems        []restPurchaseSaleItem   `json:"sale_items"`
	Items            []PurchaseLine           `json:"items"`
}

type PurchaseLine struct {
	ID          UUID    `json:"id"`
	ProductID   UUID    `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitCost    float64 `json:"unit_cost"`
}

type PurchaseCharge struct {
	ID              UUID                       `json:"id"`
	PurchaseID      UUID                       `json:"purchase_id"`
	Kind            toolkit.PurchaseChargeKind `json:"kind"`
	Amount          float64                    `json:"amount"`
	Description     *string                    `json:"description,omitempty"`
	PayeeSupplierID *UUID                      `json:"payee_supplier_id,omitempty"`
	PayeeName       *string                    `json:"payee_name,omitempty"`
	ObligationID    *UUID                      `json:"obligation_id,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
}

type PurchaseComposition struct {
	GoodsTotal   float64          `json:"goods_total"`
	Charges      []PurchaseCharge `json:"charges"`
	ChargesTotal float64          `json:"charges_total"`
	Total        float64          `json:"total"`
}

type PurchaseCostLine struct {
	LineType         toolkit.PurchaseLineType `json:"line_type"`
	LineID           UUID                     `json:"line_id"`
	ProductID        *UUID                    `json:"product_id,omitempty"`
	ProductName      string                   `json:"product_name"`
	Unit             *string                  `json:"unit,omitempty"`
	Quantity         float64                  `json:"quantity"`
	UnitCost         float64                  `json:"unit_cost"`
	BaseCost         float64                  `json:"base_cost"`
	AllocatedCharges float64                  `json:"allocated_charges"`
	LandedTotal      float64                  `json:"landed_total"`
	LandedUnitCost   float64                  `json:"landed_unit_cost"`
}

type PurchaseDetail struct {
	ID               UUID                     `json:"id"`
	Kind             toolkit.PurchaseKind     `json:"kind"`
	Status           toolkit.PurchaseStatus   `json:"status"`
	SupplierID       UUID                     `json:"supplier_id"`
	SupplierName     string                   `json:"supplier_name"`
	ExpectedDate     *Date                    `json:"expected_date,omitempty"`
	Notes            *string                  `json:"notes,omitempty"`
	OrderedAt        *Date                    `json:"ordered_at,omitempty"`
	ReceivedAt       *Date                    `json:"received_at,omitempty"`
	AllocationMethod toolkit.AllocationMethod `json:"allocation_method"`
	TotalCost        float64                  `json:"total_cost"` // Σ das pedras (= composition.goods_total)
	LineCount        int                      `json:"line_count"`
	CreatedAt        time.Time                `json:"created_at"`
	Composition      PurchaseComposition      `json:"composition"`
	Obligations      []ObligationSummary      `json:"obligations"`
	Payable          float64                  `json:"payable"`
	Paid             float64                  `json:"paid"`
	Open             float64                  `json:"open"`
	Lines            []PurchaseCostLine       `json:"lines"`
	SaleItems        []PurchaseSaleItem       `json:"sale_items"`
	Items            []PurchaseLine           `json:"items"`
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
	StockValue     float64  `json:"stock_value"` // GREATEST(balance, 0) × custo de entrada (AYD-008)
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
	ID          UUID                 `json:"id"`
	Nome        string               `json:"nome"`
	Kind        toolkit.SupplierKind `json:"kind"`
	Telefone    *string              `json:"telefone"`
	Cidade      *string              `json:"cidade"`
	Observacoes *string              `json:"observacoes"`
}

func (s legacySupplier) toEN() SupplierSummary {
	kind := s.Kind
	if kind == "" {
		kind = "STONE"
	}
	return SupplierSummary{ID: s.ID, Name: s.Nome, Kind: kind, Phone: opt(s.Telefone), City: opt(s.Cidade), Notes: opt(s.Observacoes)}
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
	ComissaoValor       float64                `json:"comissao_valor"`
	Observacoes         *string                `json:"observacoes"`
	TotalVenda          float64                `json:"total_venda"`
	TotalCusto          float64                `json:"total_custo"`
	TotalRecebido       float64                `json:"total_recebido"`
	Receivable          float64                `json:"receivable"`
	FreightPayable      float64                `json:"freight_payable"`
	CommissionPayable   float64                `json:"commission_payable"`
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
		// Frete e comissão pagos são derivados das contas a pagar da Sale (AYD-007): o que não está
		// mais a pagar foi quitado.
		FreightAmount: s.FreteValor, FreightPaid: max(s.FreteValor-s.FreightPayable, 0), FreightPayable: s.FreightPayable,
		CommissionAmount: s.ComissaoValor, CommissionPaid: s.ComissaoValor > 0 && s.CommissionPayable <= 0, Notes: opt(s.Observacoes),
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

type legacySaleDetail struct {
	legacySale
	Itens []legacySaleItem `json:"itens"`
}

// toEN devolve a Sale com os Items; os Receipts (recebimentos) são derivados da conta a receber
// (parcelas e quitações — AYD-007) e preenchidos por get_sale via withReceipts.
func (d legacySaleDetail) toEN() SaleDetail {
	out := SaleDetail{SaleSummary: d.legacySale.toEN(), Items: make([]SaleItem, 0, len(d.Itens)), Receipts: []Receipt{}}
	for _, i := range d.Itens {
		out.Items = append(out.Items, i.toEN())
	}
	return out
}
