package mcp

import (
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/mcp/tools"
)

const CatalogVersion = "1.0.0"

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
