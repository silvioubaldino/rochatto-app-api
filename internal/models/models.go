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
	Nome         string     `gorm:"not null"         json:"nome"`
	Telefone     *string    `                        json:"telefone"`
	Email        *string    `                        json:"email"`
	Endereco     *string    `                        json:"endereco"`
	Cidade       *string    `                        json:"cidade"`
	Observacoes  *string    `                        json:"observacoes"`
	LeadSource   *string    `gorm:"column:lead_source;type:lead_source_type" json:"lead_source"`
	ReferrerID   *uuid.UUID `gorm:"column:referrer_id;type:uuid"            json:"referrer_id"`
	ReferrerName *string    `gorm:"column:referrer_name;->;-:migration"      json:"referrer_name"`
}

func (Cliente) TableName() string { return "clientes" }

type Produto struct {
	Base
	Nome                string     `gorm:"not null" json:"nome"`
	Descricao           *string    `                json:"descricao"`
	PrecoReferencia     *float64   `gorm:"type:decimal(12,2)" json:"preco_referencia"`
	CustoReferencia     *float64   `gorm:"type:decimal(12,2)" json:"custo_referencia"`
	Unidade             *string    `                json:"unidade"`
	CategoryID          *uuid.UUID `gorm:"column:category_id;type:uuid"       json:"category_id"`
	CategoryName        *string    `gorm:"column:category_name;->;-:migration" json:"category_name"`
	DefaultSupplierID   *uuid.UUID `gorm:"column:default_supplier_id;type:uuid"            json:"default_supplier_id"`
	DefaultSupplierName *string    `gorm:"column:default_supplier_name;->;-:migration"     json:"default_supplier_name"`
}

func (Produto) TableName() string { return "produtos" }

type Fornecedor struct {
	Base
	Nome        string  `gorm:"not null" json:"nome"`
	Telefone    *string `               json:"telefone"`
	Cidade      *string `               json:"cidade"`
	Observacoes *string `               json:"observacoes"`
	Kind        string  `gorm:"column:kind;type:supplier_kind;not null;default:'STONE'" json:"kind"`
}

func (Fornecedor) TableName() string { return "fornecedores" }

type Partner struct {
	Base
	Name  string  `gorm:"column:nome;not null"                                json:"name"`
	Phone *string `gorm:"column:telefone"                                     json:"phone"`
	Type  string  `gorm:"column:type;type:partner_type;not null;default:'OTHER'" json:"type"`
	Notes *string `gorm:"column:observacoes"                                  json:"notes"`
}

func (Partner) TableName() string { return "vendedores_externos" }

type ProductCategory struct {
	Base
	Name string `gorm:"column:name;not null" json:"name"`
}

func (ProductCategory) TableName() string { return "product_categories" }

type Service struct {
	Base
	Name           string   `gorm:"column:name;not null"                          json:"name"`
	Unit           *string  `gorm:"column:unit"                                   json:"unit"`
	ReferencePrice *float64 `gorm:"column:reference_price;type:decimal(12,2)"     json:"reference_price"`
	ReferenceCost  *float64 `gorm:"column:reference_cost;type:decimal(12,2)"      json:"reference_cost"`
	Notes          *string  `gorm:"column:notes"                                  json:"notes"`
}

func (Service) TableName() string { return "services" }

type Venda struct {
	Base
	ClienteID           uuid.UUID  `gorm:"type:uuid;not null"                             json:"cliente_id"`
	VendedorExternoID   *uuid.UUID `gorm:"type:uuid"                                      json:"vendedor_externo_id"`
	QuoteID             *uuid.UUID `gorm:"type:uuid;uniqueIndex"                          json:"quote_id"`
	DeliveryStatus      string     `gorm:"column:delivery_status;type:sale_delivery_status;not null;default:'TO_DELIVER'" json:"delivery_status"`
	DataVenda           time.Time  `gorm:"type:date;not null"                              json:"data_venda"`
	DataEntregaPrevista *time.Time `gorm:"type:date"                                       json:"data_entrega_prevista"`
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
	Origin         *string    `gorm:"column:origin;type:item_origin"                                  json:"origin"`
	PurchaseID     *uuid.UUID `gorm:"column:purchase_id;type:uuid"                                   json:"purchase_id"`
	Kind           string     `gorm:"column:kind;type:item_kind;not null;default:'PRODUCT'"           json:"kind"`
	ServiceID      *uuid.UUID `gorm:"column:service_id;type:uuid"                                    json:"service_id"`
	CreatedAt      time.Time  `                                                        json:"created_at"`
}

func (ItemVenda) TableName() string { return "itens_venda" }

type Quote struct {
	Base
	CustomerID uuid.UUID  `gorm:"column:customer_id;type:uuid;not null"        json:"customer_id"`
	ReferrerID *uuid.UUID `gorm:"column:referrer_id;type:uuid"                 json:"referrer_id"`
	Status     string     `gorm:"column:status;type:quote_status;not null;default:'OPEN'" json:"status"`
	LostReason *string    `gorm:"column:lost_reason"                           json:"lost_reason"`
	QuoteDate  time.Time  `gorm:"column:quote_date;type:date;not null"         json:"quote_date"`
	Notes      *string    `gorm:"column:notes"                                 json:"notes"`
	// QuickSale: somente leitura via API — true só para o Quote criado por POST /quotes/quick-sale.
	QuickSale bool `gorm:"column:quick_sale;not null;default:false" json:"quick_sale"`
}

func (Quote) TableName() string { return "quotes" }

type QuoteItem struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	QuoteID     uuid.UUID  `gorm:"column:quote_id;type:uuid;not null"              json:"quote_id"`
	ProductID   *uuid.UUID `gorm:"column:product_id;type:uuid"                     json:"product_id"`
	ProductName string     `gorm:"column:product_name;not null"                    json:"product_name"`
	Quantity    float64    `gorm:"column:quantity;type:decimal(12,3);not null"     json:"quantity"`
	UnitPrice   float64    `gorm:"column:unit_price;type:decimal(12,2);not null"   json:"unit_price"`
	UnitCost    float64    `gorm:"column:unit_cost;type:decimal(12,2);not null"    json:"unit_cost"`
	Kind        string     `gorm:"column:kind;type:item_kind;not null;default:'PRODUCT'" json:"kind"`
	ServiceID   *uuid.UUID `gorm:"column:service_id;type:uuid"                           json:"service_id"`
	CreatedAt   time.Time  `                                                        json:"created_at"`
}

func (QuoteItem) TableName() string { return "quote_items" }

type QuoteCharge struct {
	Base
	QuoteID     uuid.UUID `gorm:"column:quote_id;type:uuid;not null"          json:"quote_id"`
	Kind        string    `gorm:"column:kind;type:sale_charge_kind;not null"  json:"kind"`
	Amount      float64   `gorm:"column:amount;type:decimal(12,2);not null"   json:"amount"`
	Description *string   `gorm:"column:description"                          json:"description"`
}

func (QuoteCharge) TableName() string { return "quote_charges" }

type SaleCharge struct {
	Base
	SaleID      uuid.UUID `gorm:"column:sale_id;type:uuid;not null"           json:"sale_id"`
	Kind        string    `gorm:"column:kind;type:sale_charge_kind;not null"  json:"kind"`
	Amount      float64   `gorm:"column:amount;type:decimal(12,2);not null"   json:"amount"`
	Description *string   `gorm:"column:description"                          json:"description"`
}

func (SaleCharge) TableName() string { return "sale_charges" }

type SaleCost struct {
	Base
	SaleID          uuid.UUID  `gorm:"column:sale_id;type:uuid;not null"          json:"sale_id"`
	Kind            string     `gorm:"column:kind;type:sale_cost_kind;not null"   json:"kind"`
	Amount          float64    `gorm:"column:amount;type:decimal(12,2);not null"  json:"amount"`
	Description     *string    `gorm:"column:description"                         json:"description"`
	PayeeSupplierID *uuid.UUID `gorm:"column:payee_supplier_id;type:uuid"         json:"payee_supplier_id"`
	PayeePartnerID  *uuid.UUID `gorm:"column:payee_partner_id;type:uuid"          json:"payee_partner_id"`
	SaleItemID      *uuid.UUID `gorm:"column:sale_item_id;type:uuid"              json:"sale_item_id"`
}

func (SaleCost) TableName() string { return "sale_costs" }

type Notificacao struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Tipo          string     `gorm:"not null"           json:"tipo"`
	VendaID       *uuid.UUID `gorm:"type:uuid"         json:"venda_id"`
	ObligationID  uuid.UUID  `gorm:"column:obligation_id;type:uuid;not null" json:"obligation_id"`
	InstallmentID *uuid.UUID `gorm:"column:installment_id;type:uuid"         json:"installment_id"`
	ClienteNome   string     `gorm:"not null"           json:"cliente_nome"`
	Valor         float64    `gorm:"type:decimal(12,2);not null" json:"valor"`
	DataRef       time.Time  `gorm:"type:date;not null" json:"data_ref"`
	DiasRestantes *int       `                          json:"dias_restantes"`
	Lida          bool       `gorm:"not null;default:false" json:"lida"`
	CreatedAt     time.Time  `                          json:"created_at"`
}

func (Notificacao) TableName() string { return "notificacoes" }

type Purchase struct {
	Base
	Kind             string     `gorm:"column:kind;type:purchase_kind;not null"                        json:"kind"`
	SupplierID       uuid.UUID  `gorm:"column:supplier_id;type:uuid;not null"                          json:"supplier_id"`
	Status           string     `gorm:"column:status;type:purchase_status;not null;default:'OPEN'"     json:"status"`
	ExpectedDate     *time.Time `gorm:"column:expected_date;type:date"                                 json:"expected_date"`
	OrderedAt        *time.Time `gorm:"column:ordered_at;type:date"                                    json:"ordered_at"`
	ReceivedAt       *time.Time `gorm:"column:received_at;type:date"                                   json:"received_at"`
	Notes            *string    `gorm:"column:notes"                                                   json:"notes"`
	AllocationMethod string     `gorm:"column:allocation_method;type:allocation_method;not null;default:'BY_QUANTITY'" json:"allocation_method"`
}

func (Purchase) TableName() string { return "purchases" }

type PurchaseCharge struct {
	Base
	PurchaseID      uuid.UUID  `gorm:"column:purchase_id;type:uuid;not null"                json:"purchase_id"`
	Kind            string     `gorm:"column:kind;type:purchase_charge_kind;not null"       json:"kind"`
	Amount          float64    `gorm:"column:amount;type:decimal(12,2);not null"            json:"amount"`
	Description     *string    `gorm:"column:description"                                   json:"description"`
	PayeeSupplierID *uuid.UUID `gorm:"column:payee_supplier_id;type:uuid"                   json:"payee_supplier_id"`
}

func (PurchaseCharge) TableName() string { return "purchase_charges" }

type PurchaseItem struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	PurchaseID uuid.UUID `gorm:"column:purchase_id;type:uuid;not null"           json:"purchase_id"`
	ProductID  uuid.UUID `gorm:"column:product_id;type:uuid;not null"            json:"product_id"`
	Quantity   float64   `gorm:"column:quantity;type:decimal(12,3);not null"     json:"quantity"`
	UnitCost   float64   `gorm:"column:unit_cost;type:decimal(12,2);not null"    json:"unit_cost"`
	CreatedAt  time.Time `                                                        json:"created_at"`
}

func (PurchaseItem) TableName() string { return "purchase_items" }

type StockAdjustment struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"   json:"id"`
	ProductID  uuid.UUID `gorm:"column:product_id;type:uuid;not null"              json:"product_id"`
	Quantity   float64   `gorm:"column:quantity;type:decimal(12,3);not null"       json:"quantity"`
	UnitCost   *float64  `gorm:"column:unit_cost;type:decimal(12,2)"               json:"unit_cost"`
	Reason     string    `gorm:"column:reason;type:stock_adjustment_reason;not null" json:"reason"`
	AdjustedAt time.Time `gorm:"column:adjusted_at;type:date;not null"             json:"adjusted_at"`
	Notes      *string   `gorm:"column:notes"                                      json:"notes"`
	CreatedAt  time.Time `                                                          json:"created_at"`
}

func (StockAdjustment) TableName() string { return "stock_adjustments" }
