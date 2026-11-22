
CREATE TYPE partner_type AS ENUM ('ARCHITECT', 'ENGINEER', 'BUILDER', 'OTHER');

ALTER TABLE vendedores_externos
  ADD COLUMN type partner_type NOT NULL DEFAULT 'OTHER';

CREATE TYPE lead_source_type AS ENUM ('REFERRAL', 'INSTAGRAM', 'PARTNER', 'OTHER');

ALTER TABLE clientes
  ADD COLUMN lead_source lead_source_type,
  ADD COLUMN referrer_id UUID REFERENCES vendedores_externos(id);

-- (lead_source = 'PARTNER') = (referrer_id IS NOT NULL), com lead_source NULL tratado
-- como "não é PARTNER" (sem o COALESCE, NULL = ... passaria no CHECK).
ALTER TABLE clientes
  ADD CONSTRAINT chk_clientes_lead_source_referrer
  CHECK (COALESCE(lead_source = 'PARTNER', FALSE) = (referrer_id IS NOT NULL));

CREATE INDEX idx_clientes_lead_source ON clientes(lead_source);
CREATE INDEX idx_clientes_referrer_id ON clientes(referrer_id);

CREATE TABLE product_categories (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT        NOT NULL,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX ux_product_categories_name
  ON product_categories (lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE produtos
  ADD COLUMN category_id UUID REFERENCES product_categories(id);

CREATE INDEX idx_produtos_category_id ON produtos(category_id);
