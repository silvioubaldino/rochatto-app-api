package tools

import (
	"context"
	"net/url"
	"strconv"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// AnalyticsTools: indicadores (AYD-006 §4.1 — módulo Analytics).
func AnalyticsTools() []toolkit.Tool {
	return []toolkit.Tool{getDashboardKPIs, getTopProducts, getMonthlySales}
}

// --- get_dashboard_kpis (resposta de /dashboard/kpis — AYD-005, já EN) ---

type KPISales struct {
	Count         int     `json:"count"`
	Revenue       float64 `json:"revenue"`
	Profit        float64 `json:"profit"`
	AverageTicket float64 `json:"average_ticket"`
}

type KPIQuotes struct {
	Created int `json:"created"`
	Won     int `json:"won"`
	Lost    int `json:"lost"`
	Open    int `json:"open"`
	// ConversionRate fica ausente quando não há Quotes no período (a rota devolve null).
	ConversionRate *float64 `json:"conversion_rate,omitempty"`
}

type KPIBalances struct {
	Receivable         float64 `json:"receivable"`
	PayableToSuppliers float64 `json:"payable_to_suppliers"`
	CommissionPayable  float64 `json:"commission_payable"`
	FreightPayable     float64 `json:"freight_payable"`
}

type DashboardKPIs struct {
	Period         Period      `json:"period"`
	Sales          KPISales    `json:"sales"`
	Quotes         KPIQuotes   `json:"quotes"`
	Balances       KPIBalances `json:"balances"`
	OpenDeliveries int         `json:"open_deliveries"`
}

var getDashboardKPIs = toolkit.Define(toolkit.Meta{
	Name:  "get_dashboard_kpis",
	Title: "Indicadores do período",
	Description: "Indicadores do período: Sales (vendas: quantidade, faturamento, Profit/lucro, ticket médio), " +
		"Quotes (orçamentos criados, ganhos, perdidos, abertos e taxa de conversão Quote → Sale, " +
		"sem contar vendas rápidas), saldos em aberto de hoje e entregas em aberto. Sem datas, usa " +
		"o mês corrente até hoje (fuso de São Paulo). Os números batem com get_finance_summary. " +
		"Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in PeriodIn) (DashboardKPIs, error) {
	return get[DashboardKPIs](ctx, gw, "/dashboard/kpis", in.query())
})

// --- get_top_products ---

type TopProduct struct {
	ProductID            *UUID   `json:"product_id,omitempty"`
	ProductName          string  `json:"product_name"`
	Unit                 *string `json:"unit,omitempty"`
	SalesCount           int     `json:"sales_count"`
	Quantity             float64 `json:"quantity"`
	Revenue              float64 `json:"revenue"`
	AveragePrice         float64 `json:"average_price"`
	AverageCost          float64 `json:"average_cost"`
	AverageMarginPercent float64 `json:"average_margin_percent"`
}

type GetTopProductsOut struct {
	Items []TopProduct `json:"items"`
}

var getTopProducts = toolkit.Define(toolkit.Meta{
	Name:  "get_top_products",
	Title: "Produtos mais vendidos",
	Description: "Ranking dos 20 Products (produtos) que mais faturaram no período, com quantidade vendida, " +
		"receita, preço médio e custo médio ponderados pela quantidade e margem média em %. Sem " +
		"datas, considera todo o histórico. Use para responder o que mais vende e a que preço " +
		"médio. Vendas canceladas não entram. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in PeriodIn) (GetTopProductsOut, error) {
	type legacy struct {
		ProdutoID          *UUID   `json:"produto_id"`
		ProdutoNome        string  `json:"produto_nome"`
		Unit               *string `json:"unit"`
		TotalVendas        int     `json:"total_vendas"`
		TotalQuantidade    float64 `json:"total_quantidade"`
		TotalReceita       float64 `json:"total_receita"`
		PrecoMedio         float64 `json:"preco_medio"`
		CustoMedio         float64 `json:"custo_medio"`
		MargemMediaPercent float64 `json:"margem_media_percent"`
	}
	resp, err := get[struct {
		Dados []legacy `json:"dados"`
	}](ctx, gw, "/relatorios/produtos", in.query())
	if err != nil {
		return GetTopProductsOut{}, err
	}
	out := GetTopProductsOut{Items: make([]TopProduct, 0, len(resp.Dados))}
	for _, d := range resp.Dados {
		out.Items = append(out.Items, TopProduct{
			ProductID: optUUID(d.ProdutoID), ProductName: d.ProdutoNome, Unit: opt(d.Unit), SalesCount: d.TotalVendas,
			Quantity: d.TotalQuantidade, Revenue: d.TotalReceita, AveragePrice: d.PrecoMedio,
			AverageCost: d.CustoMedio, AverageMarginPercent: d.MargemMediaPercent,
		})
	}
	return out, nil
})

// --- get_monthly_sales ---

type GetMonthlySalesIn struct {
	Months *toolkit.Months `json:"months,omitempty"`
}

type MonthlySales struct {
	Month      string  `json:"month" jsonschema:"mês no formato YYYY-MM"`
	Revenue    float64 `json:"revenue"`
	Profit     float64 `json:"profit"`
	SalesCount int     `json:"sales_count"`
}

type GetMonthlySalesOut struct {
	Items []MonthlySales `json:"items"`
}

var getMonthlySales = toolkit.Define(toolkit.Meta{
	Name:  "get_monthly_sales",
	Title: "Vendas por mês",
	Description: "Série mensal de faturamento (revenue), Profit (lucro) e número de Sales (vendas) dos últimos " +
		"N meses, incluindo o mês corrente (padrão 12, máximo 60), em ordem crescente; meses sem " +
		"vendas vêm zerados. Vendas canceladas não entram. Use para ver a evolução das vendas. " +
		"Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetMonthlySalesIn) (GetMonthlySalesOut, error) {
	q := url.Values{}
	if in.Months != nil {
		q.Set("meses", strconv.Itoa(int(*in.Months)))
	}
	type legacy struct {
		Mes        string  `json:"mes"`
		TotalVenda float64 `json:"total_venda"`
		Lucro      float64 `json:"lucro"`
		SalesCount int     `json:"sales_count"`
	}
	resp, err := get[struct {
		Dados []legacy `json:"dados"`
	}](ctx, gw, "/relatorios/vendas", q)
	if err != nil {
		return GetMonthlySalesOut{}, err
	}
	out := GetMonthlySalesOut{Items: make([]MonthlySales, 0, len(resp.Dados))}
	for _, d := range resp.Dados {
		month := d.Mes
		if len(month) >= 7 {
			month = month[:7]
		}
		out.Items = append(out.Items, MonthlySales{Month: month, Revenue: d.TotalVenda, Profit: d.Lucro, SalesCount: d.SalesCount})
	}
	return out, nil
})
