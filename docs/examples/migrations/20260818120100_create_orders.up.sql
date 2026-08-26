-- migrate:up 20260818120100 create_orders
-- NextSQL 0.1.0-dev: one statement per request; this file is split on ';'.
-- Do not include BEGIN/COMMIT/ROLLBACK.
CREATE TABLE orders (
    tenant_id   UUID NOT NULL,
    id          UUID NOT NULL DEFAULT UUID(),
    customer_id UUID NOT NULL,
    total       DECIMAL(12,2) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE INDEX ix_orders_customer ON orders (tenant_id, customer_id);
