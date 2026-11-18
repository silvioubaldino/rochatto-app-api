package oauth

import (
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfig(t *testing.T) {
	good := map[string]string{"MCP_ENABLED": "true", "PUBLIC_BASE_URL": "https://api.rochatto.com.br", "WEB_APP_URL": "https://app.rochatto.com.br"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for kk, vv := range good {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	t.Run("desligado por padrão: nada é validado", func(t *testing.T) {
		c, err := LoadConfig("production", env(map[string]string{"PUBLIC_BASE_URL": "lixo"}))
		if err != nil || c.Enabled {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})
	t.Run("válida", func(t *testing.T) {
		c, err := LoadConfig("production", env(good))
		if err != nil || !c.Enabled || c.Resource() != "https://api.rochatto.com.br/mcp" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
		if !slices.Equal(c.AllowedRedirectURIs, []string{DefaultRedirectURI}) {
			t.Errorf("default de redirect = %v", c.AllowedRedirectURIs)
		}
	})
	t.Run("porta 443 explícita é aceita", func(t *testing.T) {
		if _, err := LoadConfig("production", env(with("PUBLIC_BASE_URL", "https://api.example.com:443"))); err != nil {
			t.Error(err)
		}
	})
	bad := []struct{ name, key, val string }{
		{"MCP_ENABLED inválido", "MCP_ENABLED", "talvez"},
		{"PUBLIC_BASE_URL ausente", "PUBLIC_BASE_URL", ""},
		{"PUBLIC_BASE_URL http", "PUBLIC_BASE_URL", "http://api.example.com"},
		{"PUBLIC_BASE_URL outra porta", "PUBLIC_BASE_URL", "https://api.example.com:8443"},
		{"PUBLIC_BASE_URL barra final", "PUBLIC_BASE_URL", "https://api.example.com/"},
		{"PUBLIC_BASE_URL com path", "PUBLIC_BASE_URL", "https://api.example.com/v1"},
		{"WEB_APP_URL ausente", "WEB_APP_URL", ""},
		{"WEB_APP_URL http em produção", "WEB_APP_URL", "http://app.example.com"},
		{"WEB_APP_URL barra final", "WEB_APP_URL", "https://app.example.com/"},
		{"redirect http fora de loopback", "MCP_ALLOWED_REDIRECT_URIS", "http://evil.example/cb"},
		{"redirect com fragmento", "MCP_ALLOWED_REDIRECT_URIS", "https://claude.ai/cb#x"},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			if _, err := LoadConfig("production", env(with(b.key, b.val))); err == nil {
				t.Errorf("esperava erro (a api não deve subir)")
			}
		})
	}
	t.Run("WEB_APP_URL http em desenvolvimento", func(t *testing.T) {
		if _, err := LoadConfig("development", env(with("WEB_APP_URL", "http://localhost:3000"))); err != nil {
			t.Error(err)
		}
	})
	t.Run("lista de redirects", func(t *testing.T) {
		c, err := LoadConfig("production", env(with("MCP_ALLOWED_REDIRECT_URIS", "https://claude.ai/api/mcp/auth_callback, http://localhost/cb")))
		if err != nil || len(c.AllowedRedirectURIs) != 2 {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})
}

func TestRedirectMatching(t *testing.T) {
	allowed := []string{DefaultRedirectURI, "http://localhost/callback"}
	ok := []string{DefaultRedirectURI, "http://localhost:53682/callback", "http://localhost/callback"}
	no := []string{
		"https://claude.ai/api/mcp/auth_callback/", "https://claude.ai/api/mcp/auth_callback?x=1",
		"https://evil.example/cb", "http://localhost:5000/other", "https://localhost:5000/callback",
		"http://127.0.0.1:5000/callback",
	}
	for _, u := range ok {
		if !redirectAllowed(allowed, u) {
			t.Errorf("%s deveria ser permitido", u)
		}
	}
	for _, u := range no {
		if redirectAllowed(allowed, u) {
			t.Errorf("%s NÃO deveria ser permitido", u)
		}
	}
}

func TestNormalizeScopes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", []string{"read", "write"}, true},
		{"read write offline_access", []string{"read", "write"}, true},
		{"write read", []string{"read", "write"}, true},
		{"read", []string{"read"}, true},
		{"offline_access", []string{"read", "write"}, true},
		{"read admin", nil, false},
	}
	for _, c := range cases {
		got, ok := normalizeScopes(parseScopes(c.in))
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("normalizeScopes(%q) = %v,%v, quero %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPKCE(t *testing.T) {
	// Vetor do RFC 7636 apêndice B.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !pkceOK(verifier, challenge) || pkceOK(verifier+"x", challenge) {
		t.Error("PKCE S256 incorreto")
	}
	if !pkceChallengeRE.MatchString(challenge) || pkceChallengeRE.MatchString("curto") {
		t.Error("formato do challenge")
	}
}

func TestTokensAreOpaqueAndHashed(t *testing.T) {
	a, b := randomToken(), randomToken()
	if a == b || len(a) != 43 { // 32 bytes em base64url sem padding
		t.Errorf("token = %q", a)
	}
	h := hashToken(a)
	if len(h) != 64 || strings.Contains(h, a) || h != hashToken(a) {
		t.Errorf("hash = %q", h)
	}
}

func TestStringArrayValue(t *testing.T) {
	v, _ := StringArray{"a", `b"c`, `d\e`}.Value()
	if v != `{"a","b\"c","d\\e"}` {
		t.Errorf("literal = %v", v)
	}
	if v, _ := StringArray(nil).Value(); v != nil {
		t.Error("nil deve virar NULL")
	}
}
