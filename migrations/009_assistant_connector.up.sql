-- 009 — Assistant connector (AYD-006@context): servidor OAuth 2.1 + auditoria do /mcp.
-- Tabelas TÉCNICAS (não são entidades de negócio): exclusão física permitida (RN-08 não se aplica).

CREATE TABLE oauth_clients (
  client_id     TEXT PRIMARY KEY,
  client_name   TEXT,
  redirect_uris TEXT[] NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at  TIMESTAMPTZ
);

CREATE TABLE oauth_connections (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id      TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  owner_uid      TEXT NOT NULL,
  owner_email    TEXT NOT NULL DEFAULT '',
  scopes         TEXT[] NOT NULL,
  resource       TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at   TIMESTAMPTZ,
  revoked_at     TIMESTAMPTZ,
  revoked_reason TEXT CHECK (revoked_reason IN ('USER', 'TOKEN_REUSE', 'CODE_REUSE', 'ALLOWLIST', 'CLIENT_REVOKE'))
);
CREATE INDEX idx_oauth_connections_client ON oauth_connections(client_id);

CREATE TABLE oauth_authorization_requests (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id        TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
  redirect_uri     TEXT NOT NULL,
  state            TEXT NOT NULL,
  code_challenge   TEXT NOT NULL,
  scopes           TEXT[] NOT NULL,
  resource         TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'DENIED')),
  code_hash        TEXT UNIQUE,
  code_expires_at  TIMESTAMPTZ,
  code_used_at     TIMESTAMPTZ,
  connection_id    UUID REFERENCES oauth_connections(id) ON DELETE SET NULL,
  expires_at       TIMESTAMPTZ NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_authreq_expires ON oauth_authorization_requests(expires_at);

CREATE TABLE oauth_tokens (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  connection_id UUID NOT NULL REFERENCES oauth_connections(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL CHECK (kind IN ('ACCESS', 'REFRESH')),
  token_hash    TEXT NOT NULL UNIQUE,
  -- Detalhe de implementação: NULL = scopes da Connection; preenchido quando o refresh
  -- REDUZ os scopes (AYD-006 §2.5) — o par seguinte herda a redução.
  scopes        TEXT[],
  expires_at    TIMESTAMPTZ NOT NULL,
  rotated_at    TIMESTAMPTZ,
  revoked_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_tokens_connection ON oauth_tokens(connection_id);
CREATE INDEX idx_oauth_tokens_expires ON oauth_tokens(expires_at);

CREATE TABLE mcp_tool_calls (
  call_id        UUID PRIMARY KEY,
  connection_id  UUID REFERENCES oauth_connections(id) ON DELETE SET NULL,
  tool           TEXT NOT NULL,
  arguments      JSONB NOT NULL DEFAULT '{}'::jsonb,
  arguments_hash TEXT NOT NULL,
  outcome        TEXT NOT NULL CHECK (outcome IN ('OK', 'ERROR')),
  error_code     TEXT,
  result         JSONB,           -- só ferramentas dedupe (AYD-006 §7)
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mcp_tool_calls_dedupe ON mcp_tool_calls(connection_id, tool, arguments_hash, created_at);
CREATE INDEX idx_mcp_tool_calls_created ON mcp_tool_calls(created_at);
