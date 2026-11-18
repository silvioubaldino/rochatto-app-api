package mcp

import (
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/mcp/tools"
)

// CatalogVersion é a catalog_version (SemVer, AYD-006 §3.9), publicada em serverInfo.version.
// "-rc.1": a SPEC-006 registra só as ferramentas de leitura; a SPEC-007 completa o catálogo
// 1.0.0 (escrita + deduplicação) e troca esta constante por "1.0.0".
const CatalogVersion = "1.0.0-rc.1"

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
