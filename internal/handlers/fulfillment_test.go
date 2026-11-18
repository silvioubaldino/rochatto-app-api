package handlers

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateItemFields_Origin(t *testing.T) {
	pid := uuid.New()
	fs, mto, bad := "FROM_STOCK", "MADE_TO_ORDER", "STOCK"
	neg := -1.0
	base := itemRequest{ProdutoNome: "Granito", Quantidade: 1, PrecoVendaUnit: 10}

	cases := []struct {
		name  string
		mut   func(*itemRequest)
		valid bool
	}{
		{"sem origin (default MADE_TO_ORDER)", func(r *itemRequest) {}, true},
		{"MADE_TO_ORDER sem produto", func(r *itemRequest) { r.Origin = &mto }, true},
		{"FROM_STOCK com produto", func(r *itemRequest) { r.Origin = &fs; r.ProdutoID = &pid }, true},
		{"FROM_STOCK sem produto", func(r *itemRequest) { r.Origin = &fs }, false},
		{"origin inválido", func(r *itemRequest) { r.Origin = &bad }, false},
		{"custo negativo", func(r *itemRequest) { r.CustoUnit = &neg }, false},
		{"custo ausente é válido", func(r *itemRequest) { r.CustoUnit = nil }, true},
		{"quantidade zero", func(r *itemRequest) { r.Quantidade = 0 }, false},
		{"sem nome", func(r *itemRequest) { r.ProdutoNome = " " }, false},
	}
	for _, c := range cases {
		req := base
		c.mut(&req)
		if got := validateItemFields(req) == ""; got != c.valid {
			t.Errorf("%s: valid=%v, want %v (%q)", c.name, got, c.valid, validateItemFields(req))
		}
	}
}

func TestValidateLine(t *testing.T) {
	pid := uuid.New()
	cases := []struct {
		req   purchaseLineRequest
		valid bool
	}{
		{purchaseLineRequest{ProductID: &pid, Quantity: 2, UnitCost: 10}, true},
		{purchaseLineRequest{ProductID: &pid, Quantity: 2, UnitCost: 0}, true},
		{purchaseLineRequest{Quantity: 2, UnitCost: 10}, false},
		{purchaseLineRequest{ProductID: &pid, Quantity: 0, UnitCost: 10}, false},
		{purchaseLineRequest{ProductID: &pid, Quantity: 1, UnitCost: -1}, false},
	}
	for i, c := range cases {
		if got := validateLine(c.req) == ""; got != c.valid {
			t.Errorf("case %d: valid=%v, want %v", i, got, c.valid)
		}
	}
}

func TestValidateAdjustment(t *testing.T) {
	pid := uuid.New()
	neg := -5.0
	cases := []struct {
		req   adjustmentRequest
		valid bool
	}{
		{adjustmentRequest{ProductID: &pid, Quantity: 3, Reason: "INITIAL"}, true},
		{adjustmentRequest{ProductID: &pid, Quantity: -3, Reason: "COUNT"}, true},
		{adjustmentRequest{ProductID: &pid, Quantity: 0, Reason: "OTHER"}, false},
		{adjustmentRequest{ProductID: &pid, Quantity: 1, Reason: "LEGACY"}, false},
		{adjustmentRequest{Quantity: 1, Reason: "OTHER"}, false},
		{adjustmentRequest{ProductID: &pid, Quantity: 1, Reason: "OTHER", UnitCost: &neg}, false},
	}
	for i, c := range cases {
		if got := validateAdjustment(c.req) == ""; got != c.valid {
			t.Errorf("case %d: valid=%v, want %v", i, got, c.valid)
		}
	}
}
