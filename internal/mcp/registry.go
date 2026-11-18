package mcp

import (
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/mcp/tools"
)

// CatalogVersion é a catalog_version (SemVer, AYD-006 §3.9), publicada em serverInfo.version.
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
