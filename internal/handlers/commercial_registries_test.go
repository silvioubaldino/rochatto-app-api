package handlers

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func strp(s string) *string { return &s }

func TestValidatePartnerRequest(t *testing.T) {
	cases := []struct {
		name    string
		req     partnerRequest
		wantErr bool
	}{
		{"valid", partnerRequest{Name: "Ana Arquiteta", Type: "ARCHITECT"}, false},
		{"all types", partnerRequest{Name: "X", Type: "BUILDER"}, false},
		{"missing name", partnerRequest{Name: "  ", Type: "OTHER"}, true},
		{"name too long", partnerRequest{Name: strings.Repeat("a", 201), Type: "OTHER"}, true},
		{"missing type", partnerRequest{Name: "X"}, true},
		{"invalid type", partnerRequest{Name: "X", Type: "architect"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			got := validatePartnerRequest(&req)
			if tc.wantErr != (got != "") {
				t.Errorf("wantErr=%v, got %q", tc.wantErr, got)
			}
		})
	}
}

func TestValidatePartnerRequestNormalizes(t *testing.T) {
	req := partnerRequest{Name: "  Ana  ", Type: " ENGINEER ", Phone: strp("  "), Notes: strp(" obs ")}
	if msg := validatePartnerRequest(&req); msg != "" {
		t.Fatalf("unexpected error %q", msg)
	}
	if req.Name != "Ana" || req.Type != "ENGINEER" || req.Phone != nil || *req.Notes != "obs" {
		t.Errorf("not normalized: %+v", req)
	}
}

func TestValidateLeadSource(t *testing.T) {
	ref := uuid.New()
	cases := []struct {
		name       string
		leadSource *string
		referrerID *uuid.UUID
		wantErr    bool
	}{
		{"both null", nil, nil, false},
		{"blank lead_source is null", strp(""), nil, false},
		{"instagram without referrer", strp("INSTAGRAM"), nil, false},
		{"referral without referrer", strp("REFERRAL"), nil, false},
		{"partner with referrer", strp("PARTNER"), &ref, false},
		{"partner without referrer", strp("PARTNER"), nil, true},
		{"referrer with other source", strp("OTHER"), &ref, true},
		{"referrer with null source", nil, &ref, true},
		{"invalid lead_source", strp("TIKTOK"), nil, true},
		{"lowercase lead_source", strp("partner"), &ref, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := clienteRequest{Nome: "C", LeadSource: tc.leadSource, ReferrerID: tc.referrerID}
			got := validateLeadSource(&req)
			if tc.wantErr != (got != "") {
				t.Errorf("wantErr=%v, got %q", tc.wantErr, got)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	if n, msg := normalizeName("name", "  Quartzito "); msg != "" || n != "Quartzito" {
		t.Errorf("got %q %q", n, msg)
	}
	if _, msg := normalizeName("name", " "); !strings.Contains(msg, "'name'") {
		t.Errorf("message should name the field, got %q", msg)
	}
	if _, msg := normalizeName("name", strings.Repeat("é", 200)); msg != "" {
		t.Errorf("200 runes should be valid, got %q", msg)
	}
}
