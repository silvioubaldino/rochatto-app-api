package handlers

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
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
	mux.HandleFunc("GET /dashboard/resumo", h.getResumo)
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
	ID            uuid.UUID `json:"id"`
	Tipo          string    `json:"tipo"`
	VendaID       *uuid.UUID `json:"venda_id"`
	ClienteNome   string    `json:"cliente_nome"`
	Valor         float64   `json:"valor"`
	DataRef       string    `json:"data_ref"`
	DiasRestantes *int      `json:"dias_restantes"`
	Lida          bool      `json:"lida"`
}

type notificacoesListResponse struct {
	Total        int                   `json:"total"`
	Notificacoes []notificacaoResponse `json:"notificacoes"`
}

type vendasPorMesItem struct {
	Mes        string  `json:"mes"`
	TotalVenda float64 `json:"total_venda"`
	Lucro      float64 `json:"lucro"`
}

type vendasPorMesResponse struct {
	Dados []vendasPorMesItem `json:"dados"`
}

type produtoRankingItem struct {
	ProdutoNome        string  `json:"produto_nome"`
	TotalVendas        int64   `json:"total_vendas"`
	TotalQuantidade    float64 `json:"total_quantidade"`
	TotalReceita       float64 `json:"total_receita"`
	PrecoMedio         float64 `json:"preco_medio"`
	CustoMedio         float64 `json:"custo_medio"`
	MargemMediaPercent float64 `json:"margem_media_percent"`
}

type produtosRankingResponse struct {
	Dados []produtoRankingItem `json:"dados"`
}

// --- Endpoints ---

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

	// 1. Total a receber (vendas não entregues: total_venda - total_recebido)
	err := h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(sub.a_receber), 0)
		FROM (
			SELECT
				COALESCE(SUM(iv.quantidade * iv.preco_venda_unit), 0)
				- COALESCE(SUM(pc.valor) FILTER (WHERE pc.status = 'recebido'), 0) AS a_receber
			FROM vendas v
			LEFT JOIN itens_venda iv ON iv.venda_id = v.id
			LEFT JOIN pagamentos_cliente pc ON pc.venda_id = v.id
			WHERE v.deleted_at IS NULL AND v.status != 'ENTREGUE'
			GROUP BY v.id
		) sub
	`).Scan(&resp.TotalAReceber).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_a_receber: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 2. Total a pagar fornecedores
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(iv.quantidade * iv.custo_unit - iv.pago_fornecedor), 0)
		FROM itens_venda iv
		JOIN vendas v ON v.id = iv.venda_id
		WHERE v.deleted_at IS NULL
	`).Scan(&resp.TotalAPagarFornecedor).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_a_pagar_fornecedor: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 3. Lucro do periodo
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(sub.lucro), 0)
		FROM (
			SELECT
				COALESCE(SUM(iv.quantidade * iv.preco_venda_unit), 0)
				- COALESCE(SUM(iv.quantidade * iv.custo_unit), 0)
				- v.frete_valor
				- v.comissao_valor AS lucro
			FROM vendas v
			LEFT JOIN itens_venda iv ON iv.venda_id = v.id
			WHERE v.deleted_at IS NULL
				AND v.data_venda >= ?
				AND v.data_venda <= ?
			GROUP BY v.id, v.frete_valor, v.comissao_valor
		) sub
	`, dataInicio, dataFim).Scan(&resp.LucroPeriodo).Error
	if err != nil {
		log.Printf("ERROR Dashboard lucro_periodo: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 4. Total vendido no periodo
	err = h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(iv.quantidade * iv.preco_venda_unit), 0)
		FROM vendas v
		JOIN itens_venda iv ON iv.venda_id = v.id
		WHERE v.deleted_at IS NULL
			AND v.data_venda >= ?
			AND v.data_venda <= ?
	`, dataInicio, dataFim).Scan(&resp.TotalVendidoPeriodo).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_vendido_periodo: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// 5. Vendas abertas (nao entregues)
	err = h.db.WithContext(ctx).
		Model(&models.Venda{}).
		Where("deleted_at IS NULL AND status != 'ENTREGUE'").
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
		WHERE deleted_at IS NULL AND comissao_valor > 0 AND comissao_paga = FALSE
	`).Scan(&resp.TotalComissoesPendentes).Error
	if err != nil {
		log.Printf("ERROR Dashboard total_comissoes_pendentes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *DashboardHandler) getNotificacoes(w http.ResponseWriter, r *http.Request) {
	var notificacoes []models.Notificacao
	err := h.db.WithContext(r.Context()).
		Where("lida = FALSE").
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
		models.WriteError(w, http.StatusInternalServerError, "erro ao gerar notificações (função pg_cron pode não estar configurada)")
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
	}

	var rows []row
	err := h.db.WithContext(r.Context()).Raw(`
		SELECT
			DATE_TRUNC('month', v.data_venda)::date AS mes,
			COALESCE(SUM(iv.quantidade * iv.preco_venda_unit), 0) AS total_venda,
			COALESCE(
				SUM(iv.quantidade * iv.preco_venda_unit)
				- SUM(iv.quantidade * iv.custo_unit)
				- SUM(v.frete_valor)
				- SUM(v.comissao_valor), 0
			) AS lucro
		FROM vendas v
		JOIN itens_venda iv ON iv.venda_id = v.id
		WHERE v.deleted_at IS NULL
			AND v.data_venda >= DATE_TRUNC('month', CURRENT_DATE) - MAKE_INTERVAL(months => ?)
		GROUP BY DATE_TRUNC('month', v.data_venda)
		ORDER BY mes ASC
	`, meses).Scan(&rows).Error
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
		})
	}

	writeJSON(w, http.StatusOK, vendasPorMesResponse{Dados: dados})
}

func (h *DashboardHandler) getProdutosMaisVendidos(w http.ResponseWriter, r *http.Request) {
	var dataInicio, dataFim *time.Time

	if di := r.URL.Query().Get("data_inicio"); di != "" {
		t, err := parseDate(di)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_inicio inválida (formato: YYYY-MM-DD)")
			return
		}
		dataInicio = &t
	}
	if df := r.URL.Query().Get("data_fim"); df != "" {
		t, err := parseDate(df)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_fim inválida (formato: YYYY-MM-DD)")
			return
		}
		dataFim = &t
	}

	type row struct {
		ProdutoNome        string  `gorm:"column:produto_nome"`
		TotalVendas        int64   `gorm:"column:total_vendas"`
		TotalQuantidade    float64 `gorm:"column:total_quantidade"`
		TotalReceita       float64 `gorm:"column:total_receita"`
		PrecoMedio         float64 `gorm:"column:preco_medio"`
		CustoMedio         float64 `gorm:"column:custo_medio"`
		MargemMediaPercent float64 `gorm:"column:margem_media_percent"`
	}

	// Build query dynamically to avoid passing untyped NULL parameters to PostgreSQL.
	baseQuery := `
		SELECT
			i.produto_nome,
			COUNT(DISTINCT v.id)                       AS total_vendas,
			SUM(i.quantidade)                          AS total_quantidade,
			SUM(i.quantidade * i.preco_venda_unit)     AS total_receita,
			AVG(i.preco_venda_unit)                    AS preco_medio,
			AVG(i.custo_unit)                          AS custo_medio,
			AVG(
				(i.preco_venda_unit - i.custo_unit) / NULLIF(i.preco_venda_unit, 0) * 100
			) AS margem_media_percent
		FROM itens_venda i
		JOIN vendas v ON v.id = i.venda_id
		WHERE v.deleted_at IS NULL`

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
		GROUP BY i.produto_nome
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
			ProdutoNome:        r.ProdutoNome,
			TotalVendas:        r.TotalVendas,
			TotalQuantidade:    r.TotalQuantidade,
			TotalReceita:       r.TotalReceita,
			PrecoMedio:         r.PrecoMedio,
			CustoMedio:         r.CustoMedio,
			MargemMediaPercent: r.MargemMediaPercent,
		})
	}

	writeJSON(w, http.StatusOK, produtosRankingResponse{Dados: dados})
}
