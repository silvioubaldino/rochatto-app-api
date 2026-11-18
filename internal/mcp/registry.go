package mcp

import (
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/mcp/tools"
)

// CatalogVersion é a catalog_version (SemVer, AYD-006 §3.9), publicada em serverInfo.version.
// 1.0.0: catálogo completo — 34 ferramentas (18 de leitura da SPEC-006 + 16 de escrita da
// SPEC-007, com deduplicação).
// 1.1.0 (AYD-007 / SPEC-008): 39 ferramentas (20 de leitura + 19 de escrita): + list_obligations,
// list_financial_movements, settle_obligations, record_expense e set_installments; record_receipt,
// confirm_receipt e record_supplier_payment descontinuadas (traduzidas; saem na 2.0.0); kind no
// Supplier; get_finance_summary e list_payment_alerts no formato de contas e movimentações.
// Mudar o catálogo muda esta versão (§3.9) e o golden.
const CatalogVersion = "1.1.0"

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
