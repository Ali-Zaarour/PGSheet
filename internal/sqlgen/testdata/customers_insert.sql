-- =============================================================
--  PGSheet 1.0.0-test: generated import script
-- =============================================================
--  Generated at     : 2026-09-01 14:22:07 +00:00
--  Source file      : customers.xlsx
--  Sheet            : Sheet1 (header row 1, data rows 2 to 4)
--  Fingerprint      : sha256:fixed-for-the-golden-file
--
--  Target           : public.customers
--  Server           : PostgreSQL 16.2
--
--  Rows to insert   : 3
--  Columns mapped   : 7
--  Columns defaulted: 1  (id)
--
--  Primary key      : id  (identity)
--                 Values assigned by the database at run time.
--  Validated        : offline (full)
--
--  NOTE: this file contains personal data in the columns name, email, phone.
--        Treat it as you would the database itself.
-- =============================================================

BEGIN;
-- SET LOCAL statement_timeout = '300s';

INSERT INTO "public"."customers"
    ("name", "email", "phone", "status", "credit_limit", "signup_date", "notes")
VALUES
    ('Acme SARL', 'contact@acme.lb', '+9611234567', 'active'::"public"."customer_status", 15000.00, '2024-03-15'::date, NULL),
    ('O''Brien & Sons', 'hello@obrien.ie', '+35315550100', 'active'::"public"."customer_status", 2500.50, '2024-04-01'::date, 'Pays in EUR'),
    ('Zeta \ Partners', 'z@zeta.example', '0300000000', 'inactive'::"public"."customer_status", 0.00, '2023-12-31'::date, 'Backslash in the name');   -- rows 2-4

COMMIT;

-- =============================================================
--  End of script: 3 rows across 1 statement(s)
-- =============================================================
