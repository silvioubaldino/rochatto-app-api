-- ATENÇÃO (down): custos LABOR/OTHER, cobranças e credores se perdem (ver docs/ops/database.md).

CREATE TYPE sale_charge_kind AS ENUM ('FREIGHT', 'OTHER');
CREATE TYPE sale_cost_kind   AS ENUM ('FREIGHT', 'LABOR', 'COMMISSION', 'OTHER');

CREATE TABLE quote_charges (
  id          UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id    UUID             NOT NULL REFERENCES quotes(id),
  kind        sale_charge_kind NOT NULL,
  amount      DECIMAL(12,2)    NOT NULL CHECK (amount > 0),
  description TEXT,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_quote_charges_quote_id ON quote_charges(quote_id) WHERE deleted_at IS NULL;

CREATE TABLE sale_charges (
  id          UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
  sale_id     UUID             NOT NULL REFERENCES vendas(id),
  kind        sale_charge_kind NOT NULL,
  amount      DECIMAL(12,2)    NOT NULL CHECK (amount > 0),
  description TEXT,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sale_charges_sale_id ON sale_charges(sale_id) WHERE deleted_at IS NULL;

CREATE TABLE sale_costs (
  id                UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
  sale_id           UUID           NOT NULL REFERENCES vendas(id),
  kind              sale_cost_kind NOT NULL,
  amount            DECIMAL(12,2)  NOT NULL CHECK (amount >= 0),
  description       TEXT,
  payee_supplier_id UUID           REFERENCES fornecedores(id),
  payee_partner_id  UUID           REFERENCES vendedores_externos(id),
  sale_item_id      UUID           REFERENCES itens_venda(id),
  deleted_at        TIMESTAMPTZ,
  created_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
  CONSTRAINT chk_sale_costs_one_payee CHECK (num_nonnulls(payee_supplier_id, payee_partner_id) <= 1)
);
CREATE INDEX idx_sale_costs_sale_id       ON sale_costs(sale_id)           WHERE deleted_at IS NULL;
CREATE INDEX idx_sale_costs_payee_sup     ON sale_costs(payee_supplier_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_sale_costs_payee_partner ON sale_costs(payee_partner_id)  WHERE deleted_at IS NULL;

CREATE TEMP TABLE mig_before AS
SELECT id, amount FROM vw_obligations WHERE sale_id IS NOT NULL;

-- frete_valor → custo FREIGHT sem credor ("a definir"); comissao_valor → custo COMMISSION do
-- Partner indicador (mesma chave de conta que a 010 usava: o credor nulo continua na mesma conta).
INSERT INTO sale_costs (sale_id, kind, amount, description, created_at)
SELECT v.id, 'FREIGHT', v.frete_valor, 'Migração: frete', v.created_at
FROM vendas v WHERE v.frete_valor > 0;

INSERT INTO sale_costs (sale_id, kind, amount, description, payee_partner_id, created_at)
SELECT v.id, 'COMMISSION', v.comissao_valor, 'Migração: comissão', v.vendedor_externo_id, v.created_at
FROM vendas v WHERE v.comissao_valor > 0;

DROP VIEW IF EXISTS vw_vendas_resumo;

-- Componentes das contas: sai LEGACY_FREIGHT / LEGACY_COMMISSION; entram SALE_CHARGE e SALE_COST.
-- `amount` zera numa Sale CANCELED (a conta sai dos totais); `base_amount` é o valor "de verdade"
-- e a existência do componente decide a existência da conta.
CREATE OR REPLACE VIEW vw_obligation_components AS
SELECT
  'SALE'::text AS origin_type, v.id AS origin_id,
  'RECEIVABLE'::text AS direction, 'CUSTOMER'::text AS counterparty_type, v.cliente_id AS counterparty_id,
  'SALE_ITEMS'::text AS type, v.id AS component_id, 'Itens da venda'::text AS label,
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE ROUND(it.total, 2) END)::numeric(12,2) AS amount,
  ROUND(it.total, 2)::numeric(12,2) AS base_amount
FROM vendas v
JOIN (
  SELECT venda_id, SUM(quantidade * preco_venda_unit) AS total FROM itens_venda GROUP BY venda_id
) it ON it.venda_id = v.id
WHERE v.deleted_at IS NULL AND ROUND(it.total, 2) > 0
UNION ALL
SELECT
  'SALE', v.id, 'RECEIVABLE', 'CUSTOMER', v.cliente_id,
  'SALE_CHARGE', c.id,
  (CASE c.kind WHEN 'FREIGHT' THEN 'Frete cobrado' ELSE 'Outras cobranças' END
    || COALESCE(' · ' || NULLIF(btrim(c.description), ''), ''))::text,
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE ROUND(c.amount, 2) END)::numeric(12,2),
  ROUND(c.amount, 2)::numeric(12,2)
FROM sale_charges c
JOIN vendas v ON v.id = c.sale_id AND v.deleted_at IS NULL
WHERE c.deleted_at IS NULL
UNION ALL
SELECT
  'SALE', v.id, 'PAYABLE',
  CASE WHEN c.payee_supplier_id IS NOT NULL THEN 'SUPPLIER' WHEN c.payee_partner_id IS NOT NULL THEN 'PARTNER' END,
  COALESCE(c.payee_supplier_id, c.payee_partner_id),
  'SALE_COST', c.id,
  (CASE c.kind WHEN 'FREIGHT' THEN 'Frete pago' WHEN 'LABOR' THEN 'Mão de obra'
               WHEN 'COMMISSION' THEN 'Comissão' ELSE 'Outro custo' END
    || COALESCE(' · ' || NULLIF(btrim(c.description), ''), ''))::text,
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE ROUND(c.amount, 2) END)::numeric(12,2),
  ROUND(c.amount, 2)::numeric(12,2)
FROM sale_costs c
JOIN vendas v ON v.id = c.sale_id AND v.deleted_at IS NULL
WHERE c.deleted_at IS NULL AND c.amount > 0
UNION ALL
SELECT
  'PURCHASE', pc.purchase_id, 'PAYABLE', 'SUPPLIER', pc.supplier_id,
  'PURCHASE_ITEMS', pc.purchase_id, 'Itens da compra',
  ROUND(pc.total_cost, 2)::numeric(12,2), ROUND(pc.total_cost, 2)::numeric(12,2)
FROM vw_purchase_costs pc
WHERE ROUND(pc.total_cost, 2) > 0
UNION ALL
SELECT
  'PURCHASE', c.purchase_id, 'PAYABLE',
  CASE WHEN c.payee_supplier_id IS NOT NULL THEN 'SUPPLIER' END, c.payee_supplier_id,
  'PURCHASE_CHARGE', c.id,
  (CASE c.kind WHEN 'FREIGHT' THEN 'Frete de compra' WHEN 'UNLOADING' THEN 'Descarga'
               WHEN 'TAX' THEN 'Imposto' ELSE 'Outros' END
    || COALESCE(' · ' || NULLIF(btrim(c.description), ''), ''))::text,
  ROUND(c.amount, 2)::numeric(12,2), ROUND(c.amount, 2)::numeric(12,2)
FROM purchase_charges c
JOIN purchases p ON p.id = c.purchase_id AND p.deleted_at IS NULL
WHERE c.deleted_at IS NULL;

-- Triggers: qualquer escrita em cobranças/custos re-sincroniza as contas da Sale.
CREATE FUNCTION trg_sync_obligations_sale_component() RETURNS trigger AS $$
BEGIN
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    PERFORM sp_sync_obligations('SALE', NEW.sale_id);
  END IF;
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    PERFORM sp_sync_obligations('SALE', OLD.sale_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE OR DELETE ON sale_charges
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_sale_component();
CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE OR DELETE ON sale_costs
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_sale_component();

CREATE OR REPLACE FUNCTION trg_sync_obligations_sale() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.vendedor_externo_id IS DISTINCT FROM OLD.vendedor_externo_id THEN
    UPDATE sale_costs
       SET payee_partner_id = NEW.vendedor_externo_id
     WHERE sale_id = NEW.id AND kind = 'COMMISSION' AND deleted_at IS NULL
       AND payee_supplier_id IS NULL
       AND payee_partner_id IS NOT DISTINCT FROM OLD.vendedor_externo_id;
  END IF;
  PERFORM sp_sync_obligations('SALE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

SELECT sp_sync_obligations('SALE', id) FROM vendas;

DO $$
DECLARE
  v_bad INT;
BEGIN
  SELECT COUNT(*) INTO v_bad
  FROM mig_before b
  FULL JOIN (SELECT id, amount FROM vw_obligations WHERE sale_id IS NOT NULL) a ON a.id = b.id
  WHERE COALESCE(b.amount, 0) <> COALESCE(a.amount, 0);
  IF v_bad > 0 THEN
    RAISE EXCEPTION 'migração 012: valor das contas da venda divergente em % conta(s)', v_bad;
  END IF;
END $$;
DROP TABLE mig_before;

ALTER TABLE vendas
  DROP COLUMN frete_valor,
  DROP COLUMN comissao_valor;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)                                           AS items_total,
  COALESCE(ch.total, 0)                                                 AS charges_total,
  COALESCE(it.total_venda, 0) + COALESCE(ch.total, 0)                   AS total_venda,
  COALESCE(it.total_custo, 0) + COALESCE(ac.allocated, 0)               AS total_custo,
  COALESCE(sc.total, 0)                                                 AS costs_total,
  COALESCE(sc.freight, 0)                                               AS costs_freight,
  COALESCE(sc.commission, 0)                                            AS costs_commission,
  COALESCE(sc.total, 0) - COALESCE(sc.freight, 0) - COALESCE(sc.commission, 0) AS costs_other,
  COALESCE(rc.settled, 0)                                               AS total_recebido,
  COALESCE(it.total_venda, 0) + COALESCE(ch.total, 0)
    - COALESCE(it.total_custo, 0)
    - COALESCE(ac.allocated, 0)
    - COALESCE(sc.total, 0)                                             AS lucro,
  CASE
    WHEN COALESCE(rc.settled, 0) <= 0                                                  THEN 'PENDING'
    WHEN COALESCE(rc.settled, 0) < ROUND(COALESCE(it.total_venda, 0) + COALESCE(ch.total, 0), 2) THEN 'PARTIAL'
    ELSE 'PAID'
  END                                                                   AS payment_status,
  COALESCE(rc.open, 0)                                                  AS receivable,
  COALESCE(pc.settled, 0)                                               AS costs_paid,
  COALESCE(pc.open, 0)                                                  AS costs_open
FROM vendas v
LEFT JOIN (
  SELECT
    venda_id,
    SUM(quantidade * preco_venda_unit) AS total_venda,
    SUM(quantidade * custo_unit)       AS total_custo
  FROM itens_venda
  GROUP BY venda_id
) it ON it.venda_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(amount) AS total FROM sale_charges WHERE deleted_at IS NULL GROUP BY sale_id
) ch ON ch.sale_id = v.id
LEFT JOIN (
  SELECT sale_id,
         SUM(amount)                                      AS total,
         SUM(amount) FILTER (WHERE kind = 'FREIGHT')      AS freight,
         SUM(amount) FILTER (WHERE kind = 'COMMISSION')   AS commission
  FROM sale_costs WHERE deleted_at IS NULL GROUP BY sale_id
) sc ON sc.sale_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(allocated_charges) AS allocated
  FROM vw_purchase_line_costs
  WHERE line_type = 'SALE_ITEM'
  GROUP BY sale_id
) ac ON ac.sale_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'RECEIVABLE'
  GROUP BY sale_id
) rc ON rc.sale_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'PAYABLE' AND sale_id IS NOT NULL
  GROUP BY sale_id
) pc ON pc.sale_id = v.id
WHERE v.deleted_at IS NULL;

-- Quote: total_quote passa a incluir as cobranças (preço ao cliente). quote_items não tem fan-out
-- com quote_charges porque ambos são pré-agregados.
DROP VIEW IF EXISTS vw_quotes_resumo;
CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(qi.total, 0)                      AS items_total,
  COALESCE(qc.total, 0)                      AS charges_total,
  COALESCE(qi.total, 0) + COALESCE(qc.total, 0) AS total_quote
FROM quotes q
LEFT JOIN (
  SELECT quote_id, SUM(quantity * unit_price) AS total FROM quote_items GROUP BY quote_id
) qi ON qi.quote_id = q.id
LEFT JOIN (
  SELECT quote_id, SUM(amount) AS total FROM quote_charges WHERE deleted_at IS NULL GROUP BY quote_id
) qc ON qc.quote_id = q.id
WHERE q.deleted_at IS NULL;
