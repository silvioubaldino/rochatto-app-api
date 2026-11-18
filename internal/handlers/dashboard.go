package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

// --- Handler ---

type DashboardHandler struct {
	db *gorm.DB
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{db: db}
}

func (h *DashboardHandler) Register(mux *http.ServeMux) {
	// Dashboard
	mux.HandleFunc("GET /dashboard/kpis", h.getKPIs)
	mux.HandleFunc("GET /dashboard/resumo", h.getResumo) // deprecated (AYD-005): use /dashboard/kpis
	mux.HandleFunc("GET /dashboard/notificacoes", h.getNotificacoes)
	mux.HandleFunc("POST /dashboard/notificacoes/{id}/lida", h.marcarNotificacaoLida)
	mux.HandleFunc("POST /dashboard/notificacoes/marcar-todas-lidas", h.marcarTodasLidas)
	mux.HandleFunc("POST /dashboard/gerar-notificacoes", h.gerarNotificacoes)

	// Relatorios
	mux.HandleFunc("GET /relatorios/vendas", h.getVendasPorMes)
	mux.HandleFunc("GET /relatorios/produtos", h.getProdutosMaisVendidos)
}

// --- Response types ---

type periodoResponse struct {
	Inicio string `json:"inicio"`
	Fim    string `json:"fim"`
}

type resumoResponse struct {
	TotalAReceber           float64         `json:"total_a_receber"`
	TotalAPagarFornecedor   float64         `json:"total_a_pagar_fornecedor"`
	TotalComissoesPendentes float64         `json:"total_comissoes_pendentes"`
	LucroPeriodo            float64         `json:"lucro_periodo"`
	TotalVendidoPeriodo     float64         `json:"total_vendido_periodo"`
	VendasAbertas           int64           `json:"vendas_abertas"`
	Periodo                 periodoResponse `json:"periodo"`
}

type notificacaoResponse struct {
	ID            uuid.UUID  `json:"id"`
	Tipo          string     `json:"tipo"`
	VendaID       *uuid.UUID `json:"venda_id"`
	PagamentoID   *uuid.UUID `json:"pagamento_id"` // Receipt do alerta (SPEC-006: usado pelo Assistant connector)
	ClienteNome   string     `json:"cliente_nome"`
	Valor         float64    `json:"valor"`
	DataRef       string     `json:"data_ref"`
	DiasRestantes *int       `json:"dias_restantes"`
	Lida          bool       `json:"lida"`
}

type notificacoesListResponse struct {
	Total        int                   `json:"total"`
	Notificacoes []notificacaoResponse `json:"notificacoes"`
}

type vendasPorMesItem struct {
	Mes        string  `json:"mes"`
	TotalVenda float64 `json:"total_venda"`
	Lucro      float64 `json:"lucro"`
	SalesCount int64   `json:"sales_count"`
}

type vendasPorMesResponse struct {
	Dados []vendasPorMesItem `json:"dados"`
}

type produtoRankingItem struct {
	ProdutoID          *uuid.UUID `json:"produto_id"` // null para Items legados sem Product
	ProdutoNome        string     `json:"produto_nome"`
	TotalVendas        int64      `json:"total_vendas"`
	Unit               *string    `json:"unit"` // produtos.unidade; null em Item legado sem Product
	TotalQuantidade    float64    `json:"total_quantidade"`
	TotalReceita       float64    `json:"total_receita"`
	PrecoMedio         float64    `json:"preco_medio"`
	CustoMedio         float64    `json:"custo_medio"`
	MargemMediaPercent float64    `json:"margem_media_percent"`
}

type produtosRankingResponse struct {
	Dados []produtoRankingItem `json:"dados"`
}

// KPIs do dashboard (AYD-005).

type kpiSales struct {
	Count         int64   `json:"count"`
	Revenue       float64 `json:"revenue"`
	Profit        float64 `json:"profit"`
	AverageTicket float64 `json:"average_ticket"`
}

type kpiQuotes struct {
	Created        int64    `json:"created" gorm:"column:created"`
	Won            int64    `json:"won" gorm:"column:won"`
	Lost           int64    `json:"lost" gorm:"column:lost"`
	Open           int64    `json:"open" gorm:"column:open"`
	ConversionRate *float64 `json:"conversion_rate" gorm:"-"`
}

type kpiBalances struct {
	Receivable         float64 `json:"receivable"`
	PayableToSuppliers float64 `json:"payable_to_suppliers"`
	CommissionPayable  float64 `json:"commission_payable"`
	FreightPayable     float64 `json:"freight_payable"`
}

type kpisResponse struct {
	Period         financePeriod `json:"period"`
	Sales          kpiSales      `json:"sales"`
	Quotes         kpiQuotes     `json:"quotes"`
	Balances       kpiBalances   `json:"balances"`
	OpenDeliveries int64         `json:"open_deliveries"`
}

// getKPIs: GET /dashboard/kpis?from&to. Os blocos sales e balances vêm das MESMAS funções
// de /finance/summary (querySalesBlock / queryBalances) — dashboard e Financeiro não divergem.
func (h *DashboardHandler) getKPIs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := finance.ResolvePeriod(q.Get("from"), q.Get("to"), today())
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	db := h.db.WithContext(r.Context())
	fail := func(what string, err error) {
		log.Printf("ERROR DashboardKPIs %s: %v", what, err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
	}

	var sales financeSales
	if err := querySalesBlock(db, from, to, &sales); err != nil {
		fail("sales", err)
		return
	}
	var bal financeBalances
	if what, err := queryBalances(db, &bal); err != nil {
		fail(what, err)
		return
	}

	// Coorte de Quotes emitidos no período (quick sale fica de fora: não houve negociação).
	var quotes kpiQuotes
	if err := db.Raw(`
		SELECT
			COUNT(*)                                  AS created,
			COUNT(*) FILTER (WHERE status = 'WON')    AS won,
			COUNT(*) FILTER (WHERE status = 'LOST')   AS lost,
			COUNT(*) FILTER (WHERE status = 'OPEN')   AS open
		FROM quotes
		WHERE deleted_at IS NULL AND quick_sale = FALSE
			AND quote_date >= ? AND quote_date <= ?
	`, from, to).Scan(&quotes).Error; err != nil {
		fail("quotes", err)
		return
	}
	quotes.ConversionRate = finance.ConversionRate(quotes.Won, quotes.Created)

	var openDeliveries int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM vendas WHERE deleted_at IS NULL AND delivery_status = 'TO_DELIVER'
	`).Scan(&openDeliveries).Error; err != nil {
		fail("open_deliveries", err)
		return
	}

	writeJSON(w, http.StatusOK, kpisResponse{
		Period: financePeriod{From: from.Format("2006-01-02"), To: to.Format("2006-01-02")},
		Sales: kpiSales{
			Count: sales.Count, Revenue: sales.Revenue, Profit: sales.Profit,
			AverageTicket: finance.AverageTicket(sales.Revenue, sales.Count),
		},
		Quotes: quotes,
		Balances: kpiBalances{
			Receivable: bal.Receivable, PayableToSuppliers: bal.PayableToSuppliers,
			CommissionPayable: bal.CommissionPayable, FreightPayable: bal.FreightPayable,
		},
		OpenDeliveries: openDeliveries,
	})
}

// --- Endpoints ---

// getResumo é DEPRECATED (AYD-005): mantido sem mudanças; use GET /dashboard/kpis.
func (h *DashboardHandler) getResumo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse period filter — default: current month.
	now := time.Now()
	dataInicio := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	dataFim := now

	if di := r.URL.Query().Get("data_inicio"); di != "" {
		t, err := parseDate(di)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_inicio inválida (formato: YYYY-MM-DD)")
			return
		}
		dataInicio = t
	}
	if df := r.URL.Query().Get("data_fim"); df != "" {
		t, err := parseDate(df)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_fim inválida (formato: YYYY-MM-DD)")
			return
		}
		dataFim = t
	}

	var resp resumoResponse
	resp.Periodo = periodoResponse{
		Inicio: dataInicio.Format("2006-01-02"),
		Fim:    dataFim.Format("2006-01-02"),
	}

	// Totais por Sale vêm de vw_vendas_resumo (itens e recebimentos pré-agregados por Sale —
	// sem fan-out; a view já exclui Sales com deleted_at). Sales CANCELED saem de todos os
	// totais (AYD-002 / RN-15).

	// 1. Total a receber (RN-05: total_venda − total_recebido de toda Sale, entregue ou não;
	// pagamento a maior numa Sale não abate o saldo de outra)
	err := h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(GREATEST(vr.total_venda - vr.total_recebido, 0)), 0)
		FROM vw_vendas_resumo vr
		WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&resp.TotalAReceber).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_a_receber: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 2. Total a pagar a Suppliers (RN-06): Σ to_pay das Purchases não excluídas
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(pr.to_pay), 0)
		FROM vw_purchases_resumo pr
	`).Scan(&resp.TotalAPagarFornecedor).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_a_pagar_fornecedor: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 3. Lucro do periodo (RN-04: receita − custo − frete − comissão, uma vez por Sale)
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(vr.lucro), 0)
		FROM vw_vendas_resumo vr
		WHERE vr.data_venda >= ?
			AND vr.data_venda <= ?
			AND vr.delivery_status <> 'CANCELED'
	`, dataInicio, dataFim).Scan(&resp.LucroPeriodo).Error
	if err != nil {
		log.Printf("ERROR Dashboard lucro_periodo: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 4. Total vendido no periodo
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(vr.total_venda), 0)
		FROM vw_vendas_resumo vr
		WHERE vr.data_venda >= ?
			AND vr.data_venda <= ?
			AND vr.delivery_status <> 'CANCELED'
	`, dataInicio, dataFim).Scan(&resp.TotalVendidoPeriodo).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_vendido_periodo: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 5. Vendas abertas (a entregar)
	err = h.db.WithContext(ctx).
		Model(&models.Venda{}).
		Where("delivery_status = 'TO_DELIVER'").
		Count(&resp.VendasAbertas).Error
	if err != nil {
		log.Printf("ERROR Dashboard vendas_abertas: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 6. Comissoes pendentes
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(comissao_valor), 0)
		FROM vendas
		WHERE deleted_at IS NULL AND delivery_status <> 'CANCELED'
			AND comissao_valor > 0 AND comissao_paga = FALSE
	`).Scan(&resp.TotalComissoesPendentes).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_comissoes_pendentes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// getNotificacoes gera os alertas antes de ler (AYD-004: sem pg_cron). A função SQL é
// idempotente e preserva `lida`. Se a geração falhar, responde 500: alertas desatualizados
// (ex.: Receipt já recebido ainda aparecendo) seriam piores que um erro visível.
func (h *DashboardHandler) getNotificacoes(w http.ResponseWriter, r *http.Request) {
	if err := h.db.WithContext(r.Context()).Exec("SELECT gerar_notificacoes_pagamentos()").Error; err != nil {
		log.Printf("ERROR GetNotificacoes gerar: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// only_unread=false (SPEC-006, aditivo) inclui os alertas já lidos; o padrão segue só os não lidos.
	query := h.db.WithContext(r.Context())
	if r.URL.Query().Get("only_unread") != "false" {
		query = query.Where("lida = FALSE")
	}
	var notificacoes []models.Notificacao
	err := query.
		Order("CASE WHEN tipo = 'pagamento_vencido' THEN 0 ELSE 1 END, data_ref ASC").
		Limit(50).
		Find(&notificacoes).Error
	if err != nil {
		log.Printf("ERROR GetNotificacoes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	items := make([]notificacaoResponse, 0, len(notificacoes))
	for _, n := range notificacoes {
		items = append(items, notificacaoResponse{
			ID:            n.ID,
			Tipo:          n.Tipo,
			VendaID:       n.VendaID,
			PagamentoID:   n.PagamentoID,
			ClienteNome:   n.ClienteNome,
			Valor:         n.Valor,
			DataRef:       n.DataRef.Format("2006-01-02"),
			DiasRestantes: n.DiasRestantes,
			Lida:          n.Lida,
		})
	}

	writeJSON(w, http.StatusOK, notificacoesListResponse{
		Total:        len(items),
		Notificacoes: items,
	})
}

func (h *DashboardHandler) marcarNotificacaoLida(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	result := h.db.WithContext(r.Context()).
		Model(&models.Notificacao{}).
		Where("id = ?", id).
		Update("lida", true)
	if result.Error != nil {
		log.Printf("ERROR MarcarNotificacaoLida: %v", result.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if result.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "notificação não encontrada")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DashboardHandler) marcarTodasLidas(w http.ResponseWriter, r *http.Request) {
	if err := h.db.WithContext(r.Context()).
		Model(&models.Notificacao{}).
		Where("lida = FALSE").
		Update("lida", true).Error; err != nil {
		log.Printf("ERROR MarcarTodasLidas: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DashboardHandler) gerarNotificacoes(w http.ResponseWriter, r *http.Request) {
	if err := h.db.WithContext(r.Context()).Exec("SELECT gerar_notificacoes_pagamentos()").Error; err != nil {
		log.Printf("ERROR GerarNotificacoes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DashboardHandler) getVendasPorMes(w http.ResponseWriter, r *http.Request) {
	meses := 12
	if m := r.URL.Query().Get("meses"); m != "" {
		n, err := strconv.Atoi(m)
		if err != nil || n < 1 || n > 60 {
			models.WriteError(w, http.StatusBadRequest, "parâmetro 'meses' deve ser um número entre 1 e 60")
			return
		}
		meses = n
	}

	type row struct {
		Mes        time.Time `gorm:"column:mes"`
		TotalVenda float64   `gorm:"column:total_venda"`
		Lucro      float64   `gorm:"column:lucro"`
		SalesCount int64     `gorm:"column:sales_count"`
	}

	// EXATAMENTE N meses, do (mês corrente − N + 1) ao mês corrente (São Paulo), crescente;
	// meses sem Sale vêm zerados (generate_series LEFT JOIN agregado por mês). Uma linha por
	// Sale (vw_vendas_resumo): frete e comissão entram uma vez por Sale, não por Item.
	now := today()
	last := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	first := last.AddDate(0, -(meses - 1), 0)

	var rows []row
	err := h.db.WithContext(r.Context()).Raw(`
		SELECT
			m.mes::date                                  AS mes,
			ROUND(COALESCE(a.total_venda, 0), 2)         AS total_venda,
			ROUND(COALESCE(a.lucro, 0), 2)               AS lucro,
			COALESCE(a.sales_count, 0)                   AS sales_count
		FROM generate_series(CAST(? AS date), CAST(? AS date), INTERVAL '1 month') AS m(mes)
		LEFT JOIN (
			SELECT
				DATE_TRUNC('month', vr.data_venda)::date AS mes,
				SUM(vr.total_venda)                      AS total_venda,
				SUM(vr.lucro)                            AS lucro,
				COUNT(*)                                 AS sales_count
			FROM vw_vendas_resumo vr
			WHERE vr.delivery_status <> 'CANCELED'
			GROUP BY 1
		) a ON a.mes = m.mes::date
		ORDER BY m.mes ASC
	`, first, last).Scan(&rows).Error
	if err != nil {
		log.Printf("ERROR GetVendasPorMes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	dados := make([]vendasPorMesItem, 0, len(rows))
	for _, r := range rows {
		dados = append(dados, vendasPorMesItem{
			Mes:        r.Mes.Format("2006-01-02"),
			TotalVenda: r.TotalVenda,
			Lucro:      r.Lucro,
			SalesCount: r.SalesCount,
		})
	}

	writeJSON(w, http.StatusOK, vendasPorMesResponse{Dados: dados})
}

func (h *DashboardHandler) getProdutosMaisVendidos(w http.ResponseWriter, r *http.Request) {
	// from/to (AYD-005) vencem os legados data_inicio/data_fim, limite a limite.
	q := r.URL.Query()
	parseBound := func(names ...string) (*time.Time, bool) {
		for _, name := range names {
			if v := strings.TrimSpace(q.Get(name)); v != "" {
				t, err := parseDate(v)
				if err != nil {
					models.WriteError(w, http.StatusBadRequest, name+" inválida (formato: YYYY-MM-DD)")
					return nil, false
				}
				return &t, true
			}
		}
		return nil, true
	}
	dataInicio, ok := parseBound("from", "data_inicio")
	if !ok {
		return
	}
	dataFim, ok := parseBound("to", "data_fim")
	if !ok {
		return
	}
	if dataInicio != nil && dataFim != nil && dataInicio.After(*dataFim) {
		models.WriteError(w, http.StatusBadRequest, "from deve ser anterior ou igual a to")
		return
	}

	type row struct {
		ProdutoID          *uuid.UUID `gorm:"column:produto_id"`
		ProdutoNome        string     `gorm:"column:produto_nome"`
		TotalVendas        int64      `gorm:"column:total_vendas"`
		Unit               *string    `gorm:"column:unit"`
		TotalQuantidade    float64    `gorm:"column:total_quantidade"`
		TotalReceita       float64    `gorm:"column:total_receita"`
		PrecoMedio         float64    `gorm:"column:preco_medio"`
		CustoMedio         float64    `gorm:"column:custo_medio"`
		MargemMediaPercent float64    `gorm:"column:margem_media_percent"`
	}

	// Agrupa por Product (produto_id); Items legados sem produto_id caem no agrupamento por
	// nome. Médias ponderadas pela quantidade (Σ qtd·preço / Σ qtd) e margem sobre os totais
	// ((receita − custo) / receita), não média simples por linha de Item.
	// Build query dynamically to avoid passing untyped NULL parameters to PostgreSQL.
	baseQuery := `
		SELECT
			i.produto_id,
			COALESCE(MAX(p.nome), MAX(i.produto_nome))                     AS produto_nome,
			COUNT(DISTINCT v.id)                                           AS total_vendas,
			MAX(p.unidade)                                                 AS unit,
			SUM(i.quantidade)                                              AS total_quantidade,
			SUM(i.quantidade * i.preco_venda_unit)                         AS total_receita,
			COALESCE(SUM(i.quantidade * i.preco_venda_unit) / NULLIF(SUM(i.quantidade), 0), 0) AS preco_medio,
			COALESCE(SUM(i.quantidade * i.custo_unit) / NULLIF(SUM(i.quantidade), 0), 0)       AS custo_medio,
			COALESCE(
				(SUM(i.quantidade * i.preco_venda_unit) - SUM(i.quantidade * i.custo_unit))
				/ NULLIF(SUM(i.quantidade * i.preco_venda_unit), 0) * 100, 0
			) AS margem_media_percent
		FROM itens_venda i
		JOIN vendas v ON v.id = i.venda_id
		LEFT JOIN produtos p ON p.id = i.produto_id
		WHERE v.deleted_at IS NULL AND v.delivery_status <> 'CANCELED'`

	args := []any{}
	if dataInicio != nil {
		baseQuery += "\n\t\tAND v.data_venda >= ?"
		args = append(args, *dataInicio)
	}
	if dataFim != nil {
		baseQuery += "\n\t\tAND v.data_venda <= ?"
		args = append(args, *dataFim)
	}
	baseQuery += `
		GROUP BY i.produto_id, CASE WHEN i.produto_id IS NULL THEN i.produto_nome END
		ORDER BY total_receita DESC
		LIMIT 20`

	var rows []row
	err := h.db.WithContext(r.Context()).Raw(baseQuery, args...).Scan(&rows).Error
	if err != nil {
		log.Printf("ERROR GetProdutosMaisVendidos: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	dados := make([]produtoRankingItem, 0, len(rows))
	for _, r := range rows {
		dados = append(dados, produtoRankingItem{
			ProdutoID:          r.ProdutoID,
			ProdutoNome:        r.ProdutoNome,
			TotalVendas:        r.TotalVendas,
			Unit:               r.Unit,
			TotalQuantidade:    r.TotalQuantidade,
			TotalReceita:       r.TotalReceita,
			PrecoMedio:         r.PrecoMedio,
			CustoMedio:         r.CustoMedio,
			MargemMediaPercent: r.MargemMediaPercent,
		})
	}

	writeJSON(w, http.StatusOK, produtosRankingResponse{Dados: dados})
}
