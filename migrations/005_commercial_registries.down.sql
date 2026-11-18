DROP INDEX IF EXISTS idx_produtos_category_id;
ALTER TABLE produtos DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS product_categories;

DROP INDEX IF EXISTS idx_clientes_referrer_id;
DROP INDEX IF EXISTS idx_clientes_lead_source;
ALTER TABLE clientes DROP CONSTRAINT IF EXISTS chk_clientes_lead_source_referrer;
ALTER TABLE clientes DROP COLUMN IF EXISTS referrer_id;
ALTER TABLE clientes DROP COLUMN IF EXISTS lead_source;
DROP TYPE IF EXISTS lead_source_type;

ALTER TABLE vendedores_externos DROP COLUMN IF EXISTS type;
DROP TYPE IF EXISTS partner_type;
