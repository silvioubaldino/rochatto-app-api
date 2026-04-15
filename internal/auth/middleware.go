package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/silvioubaldino/sales-backend/internal/models"
)

type contextKey string

const userUIDKey contextKey = "userUID"

// UserUID returns the authenticated user's UID from the context.
func UserUID(ctx context.Context) (string, bool) {
	uid, ok := ctx.Value(userUIDKey).(string)
	return uid, ok
}

// jwksCache holds the Firebase public keys with a TTL.
type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	ttl       time.Duration
}

var cache = &jwksCache{ttl: 1 * time.Hour}

const firebaseJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if time.Since(c.fetchedAt) < c.ttl && c.keys != nil {
		key, ok := c.keys[kid]
		c.mu.RUnlock()
		if ok {
			return key, nil
		}
		return nil, fmt.Errorf("key %s not found", kid)
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock.
	if time.Since(c.fetchedAt) < c.ttl && c.keys != nil {
		key, ok := c.keys[kid]
		if ok {
			return key, nil
		}
		return nil, fmt.Errorf("key %s not found", kid)
	}

	if err := c.fetchKeys(); err != nil {
		return nil, err
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %s not found after refresh", kid)
	}
	return key, nil
}

func (c *jwksCache) fetchKeys() error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(firebaseJWKSURL)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decoding JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		pub, err := parseRSAPublicKey(k)
		if err != nil {
			return fmt.Errorf("parsing key %s: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}

	c.keys = keys
	c.fetchedAt = time.Now()
	return nil
}

func parseRSAPublicKey(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding E: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	return &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}, nil
}

// RequireAuth is an HTTP middleware that validates Firebase JWTs.
func RequireAuth(projectID string) func(http.Handler) http.Handler {
	expectedIssuer := "https://securetoken.google.com/" + projectID

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				models.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				if token.Method.Alg() != "RS256" {
					return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
				}
				kid, ok := token.Header["kid"].(string)
				if !ok {
					return nil, fmt.Errorf("missing kid header")
				}
				return cache.getKey(kid)
			},
				jwt.WithValidMethods([]string{"RS256"}),
				jwt.WithAudience(projectID),
				jwt.WithIssuer(expectedIssuer),
				jwt.WithExpirationRequired(),
			)
			if err != nil || !token.Valid {
				models.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			sub, err := token.Claims.GetSubject()
			if err != nil || sub == "" {
				models.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			ctx := context.WithValue(r.Context(), userUIDKey, sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
