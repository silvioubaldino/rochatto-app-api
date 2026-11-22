// Package oauth implementa o servidor de autorização OAuth 2.1 do Assistant connector.
package oauth

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const DefaultRedirectURI = "https://claude.ai/api/mcp/auth_callback"

type Config struct {
	Enabled             bool
	PublicBaseURL       string
	WebAppURL           string
	AllowedRedirectURIs []string
}

func (c Config) Resource() string { return c.PublicBaseURL + "/mcp" }

// LoadConfig lê MCP_ENABLED, PUBLIC_BASE_URL, WEB_APP_URL e MCP_ALLOWED_REDIRECT_URIS.
func LoadConfig(env string, get func(string) string) (Config, error) {
	var c Config
	if raw := strings.TrimSpace(get("MCP_ENABLED")); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("MCP_ENABLED inválida %q (use true/false)", raw)
		}
		c.Enabled = b
	}
	if !c.Enabled {
		return c, nil
	}
	base := strings.TrimSpace(get("PUBLIC_BASE_URL"))
	if err := validatePublicBaseURL(base); err != nil {
		return c, fmt.Errorf("PUBLIC_BASE_URL: %w", err)
	}
	c.PublicBaseURL = base

	web := strings.TrimSpace(get("WEB_APP_URL"))
	if err := validateWebAppURL(env, web); err != nil {
		return c, fmt.Errorf("WEB_APP_URL: %w", err)
	}
	c.WebAppURL = web

	raw := strings.TrimSpace(get("MCP_ALLOWED_REDIRECT_URIS"))
	if raw == "" {
		raw = DefaultRedirectURI
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if err := validateRedirectURI(item); err != nil {
			return c, fmt.Errorf("MCP_ALLOWED_REDIRECT_URIS: %q: %w", item, err)
		}
		c.AllowedRedirectURIs = append(c.AllowedRedirectURIs, item)
	}
	if len(c.AllowedRedirectURIs) == 0 {
		return c, errors.New("MCP_ALLOWED_REDIRECT_URIS: nenhum redirect_uri válido")
	}
	return c, nil
}

func validatePublicBaseURL(s string) error {
	if s == "" {
		return errors.New("obrigatória com MCP_ENABLED=true")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return errors.New("URL inválida")
	}
	if u.Scheme != "https" {
		return errors.New("deve usar https")
	}
	if p := u.Port(); p != "" && p != "443" {
		return errors.New("deve usar a porta 443")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.HasSuffix(s, "/") {
		return errors.New("deve ser só a origem, sem barra final, path, query ou fragmento")
	}
	return nil
}

func validateWebAppURL(env, s string) error {
	if s == "" {
		return errors.New("obrigatória com MCP_ENABLED=true")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return errors.New("URL inválida")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && env != "production") {
		return errors.New("deve usar https")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.HasSuffix(s, "/") {
		return errors.New("deve ser só a origem, sem barra final, path, query ou fragmento")
	}
	return nil
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "[::1]" || h == "::1"
}

func validateRedirectURI(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("URI absoluta inválida")
	}
	if u.Fragment != "" {
		return errors.New("não pode ter fragmento")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return errors.New("deve usar https (ou http em loopback)")
	}
	return nil
}

func redirectMatches(registered, given string) bool {
	if registered == given {
		return true
	}
	r, err1 := url.Parse(registered)
	g, err2 := url.Parse(given)
	if err1 != nil || err2 != nil {
		return false
	}
	if r.Scheme != "http" || g.Scheme != "http" || !isLoopbackHost(r.Hostname()) {
		return false
	}
	return r.Hostname() == g.Hostname() && r.EscapedPath() == g.EscapedPath() && r.RawQuery == g.RawQuery
}

func redirectAllowed(allowed []string, uri string) bool {
	for _, a := range allowed {
		if redirectMatches(a, uri) {
			return true
		}
	}
	return false
}
