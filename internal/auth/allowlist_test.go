package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAllowlistCheck(t *testing.T) {
	allow := ParseAllowlist(" Dono@Example.com, ,socio@example.com ")

	cases := []struct {
		name    string
		claims  jwt.MapClaims
		wantErr error
	}{
		{"e-mail na lista (case-insensitive)", jwt.MapClaims{"email": "dono@EXAMPLE.com", "email_verified": true}, nil},
		{"segundo e-mail", jwt.MapClaims{"email": "socio@example.com", "email_verified": true}, nil},
		{"fora da lista", jwt.MapClaims{"email": "intruso@example.com", "email_verified": true}, errEmailNotAllowed},
		{"não verificado", jwt.MapClaims{"email": "dono@example.com", "email_verified": false}, errEmailUnverified},
		{"sem email_verified", jwt.MapClaims{"email": "dono@example.com"}, errEmailUnverified},
		{"email_verified não-booleano", jwt.MapClaims{"email": "dono@example.com", "email_verified": "true"}, errEmailUnverified},
		{"sem e-mail", jwt.MapClaims{"email_verified": true}, errEmailMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			err := allow.Check(tc.claims)
			// Assert
			if err != tc.wantErr {
				t.Errorf("Check = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAllowlistEmptyAllowsAll(t *testing.T) {
	allow := ParseAllowlist(" , ")
	if !allow.Empty() {
		t.Fatalf("Empty = false, want true")
	}
	if err := allow.Check(jwt.MapClaims{}); err != nil {
		t.Errorf("Check com lista vazia = %v, want nil", err)
	}
}

// TestRequireAuthAllowlist exercita o middleware inteiro com uma chave RSA local injetada no
// cache de JWKS (sem rede): token válido + e-mail fora da lista → 403; na lista → 200.
func TestRequireAuthAllowlist(t *testing.T) {
	// Arrange
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const projectID, kid = "proj-test", "kid-test"
	cache.mu.Lock()
	cache.keys = map[string]*rsa.PublicKey{kid: &key.PublicKey}
	cache.fetchedAt = time.Now()
	cache.mu.Unlock()

	sign := func(email string, verified bool) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss":            "https://securetoken.google.com/" + projectID,
			"aud":            projectID,
			"sub":            "uid-1",
			"exp":            time.Now().Add(time.Hour).Unix(),
			"email":          email,
			"email_verified": verified,
		})
		tok.Header["kid"] = kid
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RequireAuth(projectID, ParseAllowlist("dono@example.com"))(ok)

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"dono", sign("DONO@example.com", true), http.StatusOK},
		{"outro e-mail", sign("outro@example.com", true), http.StatusForbidden},
		{"dono não verificado", sign("dono@example.com", false), http.StatusForbidden},
		{"token inválido", "lixo", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			// Act
			h.ServeHTTP(rec, req)
			// Assert
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestLoadAllowlist(t *testing.T) {
	if _, err := LoadAllowlist("production", ""); err != ErrAllowlistRequired {
		t.Errorf("production vazia = %v, want ErrAllowlistRequired", err)
	}
	if a, err := LoadAllowlist("production", "dono@example.com"); err != nil || a.Len() != 1 {
		t.Errorf("production com e-mail = (%d, %v), want (1, nil)", a.Len(), err)
	}
	if a, err := LoadAllowlist("development", ""); err != nil || !a.Empty() {
		t.Errorf("development vazia = (empty=%t, %v), want (true, nil)", a.Empty(), err)
	}
}

func TestAllowsEmailAndUserContext(t *testing.T) {
	a := ParseAllowlist(" Dono@Exemplo.com , outro@exemplo.com ")
	if !a.AllowsEmail("dono@exemplo.com") || !a.AllowsEmail("DONO@EXEMPLO.COM") || a.AllowsEmail("intruso@exemplo.com") || a.AllowsEmail("") {
		t.Error("AllowsEmail deve comparar sem caixa e recusar e-mails fora da lista")
	}
	if !ParseAllowlist("").AllowsEmail("qualquer@x.com") {
		t.Error("lista vazia = aberta (só desenvolvimento)")
	}
	ctx := WithUser(context.Background(), "uid-1", " Dono@Exemplo.com ")
	if uid, ok := UserUID(ctx); !ok || uid != "uid-1" {
		t.Errorf("uid = %q %v", uid, ok)
	}
	if e, ok := UserEmail(ctx); !ok || e != "dono@exemplo.com" {
		t.Errorf("email = %q %v", e, ok)
	}
	if _, ok := UserEmail(context.Background()); ok {
		t.Error("sem usuário não há e-mail")
	}
}
