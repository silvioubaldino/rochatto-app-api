package handlers

import "fmt"

// Helpers dos testes de integração para contas, quitações e movimentações (AYD-007).

// obligationsOf lista as contas de uma origem (SALE|PURCHASE).
func (c apiClient) obligationsOf(originType, originID string, extra ...string) []any {
	c.t.Helper()
	q := "/obligations?origin_type=" + originType + "&origin_id=" + originID
	for _, e := range extra {
		q += "&" + e
	}
	code, res := c.do("GET", q, nil)
	if code != 200 {
		c.t.Fatalf("GET %s = %d %v", q, code, res)
	}
	items, _ := res["items"].([]any)
	return items
}

// receivable devolve o id da conta a receber da Sale.
func (c apiClient) receivable(saleID string) string {
	c.t.Helper()
	items := c.obligationsOf("SALE", saleID, "direction=RECEIVABLE")
	if len(items) != 1 {
		c.t.Fatalf("conta a receber da venda %s: %d itens", saleID, len(items))
	}
	return items[0].(map[string]any)["id"].(string)
}

// receive recebe `amount` do cliente na Sale (quitação da conta a receber).
func (c apiClient) receive(saleID string, amount float64, day, method string) (int, map[string]any) {
	c.t.Helper()
	return c.do("POST", "/obligations/"+c.receivable(saleID)+"/settle", map[string]any{
		"amount": amount, "movement": map[string]any{"occurred_on": day, "payment_method": method},
	})
}

// plan define parcelas (plano) de uma conta: [{due_date, amount}].
func (c apiClient) plan(obligationID string, rows ...[2]any) (int, map[string]any) {
	c.t.Helper()
	ins := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		ins = append(ins, map[string]any{"due_date": r[0], "amount": r[1]})
	}
	return c.do("PUT", "/obligations/"+obligationID+"/installments", map[string]any{"installments": ins})
}

// payableOf devolve a conta a pagar da origem para o credor (supplier/partner id; "" = sem credor).
func (c apiClient) payableOf(originType, originID, counterpartyID string) string {
	c.t.Helper()
	for _, it := range c.obligationsOf(originType, originID, "direction=PAYABLE") {
		o := it.(map[string]any)
		cp, _ := o["counterparty"].(map[string]any)
		if (counterpartyID == "" && cp == nil) || (cp != nil && cp["id"] == counterpartyID) {
			return o["id"].(string)
		}
	}
	c.t.Fatalf("conta a pagar de %s %s para %q não encontrada", originType, originID, counterpartyID)
	return ""
}

func str(v any) string { return fmt.Sprint(v) }

// partner cria um Partner e devolve o id.
func (e fxEnv) partner() string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO vendedores_externos (nome) VALUES (?) RETURNING id", uniq("Parceiro")).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}
