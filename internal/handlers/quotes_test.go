package handlers

import (
	"testing"

	"github.com/silvioubaldino/sales-backend/internal/models"
)

func TestValidateQuoteItemFields(t *testing.T) {
	cases := []struct {
		name    string
		req     quoteItemRequest
		wantErr bool
	}{
		{
			name:    "valid item",
			req:     quoteItemRequest{ProductName: "Granito Preto", Quantity: 2, UnitPrice: 100, UnitCost: 60},
			wantErr: false,
		},
		{
			name:    "missing product_name",
			req:     quoteItemRequest{ProductName: "  ", Quantity: 2, UnitPrice: 100, UnitCost: 60},
			wantErr: true,
		},
		{
			name:    "quantity zero",
			req:     quoteItemRequest{ProductName: "Granito", Quantity: 0, UnitPrice: 100, UnitCost: 60},
			wantErr: true,
		},
		{
			name:    "quantity negative",
			req:     quoteItemRequest{ProductName: "Granito", Quantity: -1, UnitPrice: 100, UnitCost: 60},
			wantErr: true,
		},
		{
			name:    "negative unit_price",
			req:     quoteItemRequest{ProductName: "Granito", Quantity: 1, UnitPrice: -1, UnitCost: 60},
			wantErr: true,
		},
		{
			name:    "negative unit_cost",
			req:     quoteItemRequest{ProductName: "Granito", Quantity: 1, UnitPrice: 100, UnitCost: -1},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateQuoteItemFields(tc.req)
			if tc.wantErr && got == "" {
				t.Errorf("expected validation error, got none")
			}
			if !tc.wantErr && got != "" {
				t.Errorf("expected no validation error, got %q", got)
			}
		})
	}
}

func TestValidQuoteStatuses(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"OPEN", true},
		{"WON", true},
		{"LOST", true},
		{"FOO", false},
		{"", false},
		{"open", false}, // enum é case-sensitive (AYD-001)
	}

	for _, tc := range cases {
		if got := validQuoteStatuses[tc.status]; got != tc.want {
			t.Errorf("validQuoteStatuses[%q] = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestQuoteItemToResponse_TotalPrice(t *testing.T) {
	item := quoteItemToResponse(models.QuoteItem{
		ProductName: "Granito Preto",
		Quantity:    3,
		UnitPrice:   150,
		UnitCost:    90,
	})
	if item.TotalPrice != 450 {
		t.Errorf("TotalPrice = %v, want 450", item.TotalPrice)
	}
}
