CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT UUID(),
    customer_id UUID NOT NULL
);
