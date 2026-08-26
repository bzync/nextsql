-- migrate:up 20260818120000 create_customers
-- NextSQL 0.1.0-dev: one statement per request; this file is split on ';'.
-- Do not include BEGIN/COMMIT/ROLLBACK.
CREATE TABLE customers (
    tenant_id  UUID NOT NULL,
    id         UUID NOT NULL DEFAULT UUID(),
    email      STRING NOT NULL,
    name       STRING NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, id)
);

CREATE UNIQUE INDEX ux_customers_tenant_email ON customers (tenant_id, email);
