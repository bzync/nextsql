CREATE TABLE customers (
    id UUID PRIMARY KEY DEFAULT UUID(),
    name STRING NOT NULL
);
CREATE INDEX ix_customers_name ON customers (name);
