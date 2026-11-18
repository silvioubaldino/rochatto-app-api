package mcp

import (
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/mcp/tools"
)

// CatalogVersion é a catalog_version (SemVer, AYD-006 §3.9), publicada em serverInfo.version.
// 1.0.0: catálogo completo — 37 ferramentas (20 de leitura + 17 de escrita, com deduplicação),
// incluindo as de contas e movimentações (AYD-007 / SPEC-008): list_obligations,
// list_financial_movements, settle_obligations, record_expense e set_installments; kind no
// Supplier; add_purchase_charge e get_purchase/get_stock_balance com composição da compra e custo de
// entrada (AYD-008 / SPEC-009); get_finance_summary e list_payment_alerts no formato de contas e movimentações.
// Mudar o catálogo muda esta versão (§3.9) e o golden.
const CatalogVersion = "1.0.0"

// Catalog concatena os módulos de ferramentas (regra 4 do AYD: o registro só concatena).
// Uma feature nova declara a ferramenta no módulo do seu domínio; módulo novo entra aqui.
func Catalog() []toolkit.Tool {
	var all []toolkit.Tool
	for _, module := range [][]toolkit.Tool{
		tools.CatalogTools(),
		tools.CRMTools(),
		tools.SalesTools(),
		tools.ProcurementTools(),
		tools.StockTools(),
		tools.FinancialTools(),
		tools.AnalyticsTools(),
	} {
		all = append(all, module...)
	}
	return all
}
