-- migrate:up 20260818120200 create_lines
-- NextSQL 0.1.0-dev: one statement per request; this file is split on ';'.
-- Do not include BEGIN/COMMIT/ROLLBACK.
CREATE TABLE lines (
    tenant_id  UUID NOT NULL,
    id         UUID NOT NULL DEFAULT UUID(),
    order_id   UUID NOT NULL,
    sku        STRING NOT NULL,
    qty        DECIMAL(12,0) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT fk_lines_order
        FOREIGN KEY (tenant_id, order_id)
        REFERENCES orders (tenant_id, id)
        ON DELETE CASCADE
        ON UPDATE RESTRICT
);

CREATE INDEX ix_lines_order ON lines (tenant_id, order_id);
