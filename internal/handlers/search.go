package handlers

// Busca global (AYD-011 C9 / SPEC-012): GET /search?q=&types=&limit=. Devolve dados — a web
// formata. Casa nome (contém, sem acento nem caixa), telefone, número exato (Quote/Sale/Purchase)
// e o nome do cliente/fornecedor das operações; exclui soft-deletados. As regras do termo estão
// em internal/search.

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/search"
	"gorm.io/gorm"
)

type SearchHandler struct {
	db *gorm.DB
}

func NewSearchHandler(db *gorm.DB) *SearchHandler { return &SearchHandler{db: db} }

func (h *SearchHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /search", h.search)
}

type searchItem struct {
	ID     uuid.UUID `json:"id"`
	Number *int      `json:"number"`
	Title  string    `json:"title"`
	Date   *string   `json:"date"`
	Amount *float64  `json:"amount"`
	Status *string   `json:"status"`
	City   *string   `json:"city"`
	Kind   *string   `json:"kind"`
}

type searchGroup struct {
	Type  string       `json:"type"`
	Items []searchItem `json:"items"`
}

type searchResponse struct {
	Groups []searchGroup `json:"groups"`
}

type searchRow struct {
	ID     uuid.UUID  `gorm:"column:id"`
	Number *int       `gorm:"column:number"`
	Title  string     `gorm:"column:title"`
	Date   *time.Time `gorm:"column:date"`
	Amount *float64   `gorm:"column:amount"`
	Status *string    `gorm:"column:status"`
	City   *string    `gorm:"column:city"`
	Kind   *string    `gorm:"column:kind"`
}

func (r searchRow) toItem() searchItem {
	return searchItem{ID: r.ID, Number: r.Number, Title: r.Title, Date: formatDate(r.Date),
		Amount: round2Ptr(r.Amount), Status: r.Status, City: r.City, Kind: r.Kind}
}

// searchTerm traz o termo pronto para o SQL: padrões LIKE já normalizados e escapados.
type searchTerm struct {
	q        search.Query
	contains string
	prefix   string
}

func newSearchTerm(q search.Query) searchTerm {
	esc := search.EscapeLike(q.Text)
	return searchTerm{q: q, contains: "%" + esc + "%", prefix: esc + "%"}
}

func foldCol(col string) string { return strings.Replace(foldSQL, "%s", col, 1) }

// match: nome contém (+ telefone, + número exato) — condição e argumentos.
func (t searchTerm) match(nameCol, phoneCol, numberCol string) (string, []any) {
	conds := []string{foldCol(nameCol) + ` LIKE ? ESCAPE '\'`}
	args := []any{t.contains}
	if phoneCol != "" {
		conds = append(conds, `lower(COALESCE(`+phoneCol+`, '')) LIKE ? ESCAPE '\'`)
		args = append(args, t.contains)
		if t.q.Digits != "" {
			conds = append(conds, `regexp_replace(COALESCE(`+phoneCol+`, ''), '\D', '', 'g') LIKE ?`)
			args = append(args, "%"+t.q.Digits+"%")
		}
	}
	if numberCol != "" && t.q.Number != nil {
		conds = append(conds, numberCol+" = ?")
		args = append(args, *t.q.Number)
	}
	return "(" + strings.Join(conds, " OR ") + ")", args
}

// rank: 0 número exato, 1 começa com o termo, 2 contém.
func (t searchTerm) rank(nameCol, numberCol string) (string, []any) {
	var args []any
	sql := "CASE "
	if numberCol != "" && t.q.Number != nil {
		sql += "WHEN " + numberCol + " = ? THEN 0 "
		args = append(args, *t.q.Number)
	}
	sql += "WHEN " + foldCol(nameCol) + ` LIKE ? ESCAPE '\' THEN 1 ELSE 2 END`
	return sql, append(args, t.prefix)
}

// searchSpec descreve a consulta de um tipo.
type searchSpec struct {
	selectFrom string // SELECT ... FROM ... (sem WHERE)
	where      string // condição fixa (soft-delete)
	name       string // coluna do nome casado
	phone      string // coluna de telefone ("" = não casa)
	number     string // coluna do número ("" = sem número)
	recency    string // ORDER BY de desempate (mais recente primeiro)
}

func (s searchSpec) run(db *gorm.DB, t searchTerm, limit int) ([]searchRow, error) {
	cond, args := t.match(s.name, s.phone, s.number)
	rank, rankArgs := t.rank(s.name, s.number)
	sql := s.selectFrom + " WHERE " + s.where + " AND " + cond + " ORDER BY " + rank + ", " + s.recency + ", id LIMIT ?"
	all := append(append(append([]any{}, args...), rankArgs...), limit)
	var rows []searchRow
	err := db.Raw(sql, all...).Scan(&rows).Error
	return rows, err
}

const brDate = `(%s AT TIME ZONE 'America/Sao_Paulo')::date`

var searchSpecs = map[string]searchSpec{
	search.Customer: {
		selectFrom: `SELECT c.id, NULL::int AS number, c.nome AS title, NULL::date AS date, NULL::numeric AS amount,
			NULL::text AS status, c.cidade AS city, NULL::text AS kind FROM clientes c`,
		where: "c.deleted_at IS NULL", name: "c.nome", phone: "c.telefone", recency: "c.created_at DESC",
	},
	search.Quote: {
		selectFrom: `SELECT q.id, q.number, c.nome AS title, q.quote_date AS date, qr.total_quote AS amount,
			q.status::text AS status, c.cidade AS city, NULL::text AS kind
			FROM quotes q JOIN vw_quotes_resumo qr ON qr.id = q.id JOIN clientes c ON c.id = q.customer_id`,
		where: "q.deleted_at IS NULL", name: "c.nome", number: "q.number", recency: "q.quote_date DESC, q.created_at DESC",
	},
	search.Sale: {
		selectFrom: `SELECT v.id, v.number, c.nome AS title, v.data_venda AS date, vr.total_venda AS amount,
			v.delivery_status::text AS status, c.cidade AS city, NULL::text AS kind
			FROM vendas v JOIN vw_vendas_resumo vr ON vr.id = v.id JOIN clientes c ON c.id = v.cliente_id`,
		where: "v.deleted_at IS NULL", name: "c.nome", number: "v.number", recency: "v.data_venda DESC, v.created_at DESC",
	},
	search.Purchase: {
		selectFrom: `SELECT pu.id, pu.number, f.nome AS title, ` + strings.Replace(brDate, "%s", "pu.created_at", 1) + ` AS date,
			pr.total_cost AS amount, pu.status::text AS status, NULL::text AS city, NULL::text AS kind
			FROM purchases pu JOIN vw_purchases_resumo pr ON pr.id = pu.id JOIN fornecedores f ON f.id = pu.supplier_id`,
		where: "pu.deleted_at IS NULL", name: "f.nome", number: "pu.number", recency: "pu.created_at DESC",
	},
	search.Product: {
		selectFrom: `SELECT p.id, NULL::int AS number, p.nome AS title, NULL::date AS date, NULL::numeric AS amount,
			NULL::text AS status, NULL::text AS city, p.unidade AS kind FROM produtos p`,
		where: "p.deleted_at IS NULL", name: "p.nome", recency: "p.created_at DESC",
	},
	search.Service: {
		selectFrom: `SELECT s.id, NULL::int AS number, s.name AS title, NULL::date AS date, NULL::numeric AS amount,
			NULL::text AS status, NULL::text AS city, s.unit AS kind FROM services s`,
		where: "s.deleted_at IS NULL", name: "s.name", recency: "s.created_at DESC",
	},
	search.Supplier: {
		selectFrom: `SELECT f.id, NULL::int AS number, f.nome AS title, NULL::date AS date, NULL::numeric AS amount,
			NULL::text AS status, NULL::text AS city, f.kind::text AS kind FROM fornecedores f`,
		where: "f.deleted_at IS NULL", name: "f.nome", phone: "f.telefone", recency: "f.created_at DESC",
	},
	search.Partner: {
		selectFrom: `SELECT p.id, NULL::int AS number, p.nome AS title, NULL::date AS date, NULL::numeric AS amount,
			NULL::text AS status, NULL::text AS city, p.type::text AS kind FROM vendedores_externos p`,
		where: "p.deleted_at IS NULL", name: "p.nome", phone: "p.telefone", recency: "p.created_at DESC",
	},
}

func (h *SearchHandler) search(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	q, err := search.ParseQuery(params.Get("q"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	types, err := search.ParseTypes(params.Get("types"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := search.ParseLimit(params.Get("limit"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	term := newSearchTerm(q)
	db := h.db.WithContext(r.Context())
	resp := searchResponse{Groups: []searchGroup{}}
	for _, typ := range types {
		rows, err := searchSpecs[typ].run(db, term, limit)
		if err != nil {
			log.Printf("ERROR Search %s: %v", typ, err)
			models.WriteError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		if len(rows) == 0 {
			continue
		}
		g := searchGroup{Type: typ, Items: make([]searchItem, 0, len(rows))}
		for _, row := range rows {
			g.Items = append(g.Items, row.toItem())
		}
		resp.Groups = append(resp.Groups, g)
	}
	writeJSON(w, http.StatusOK, resp)
}
