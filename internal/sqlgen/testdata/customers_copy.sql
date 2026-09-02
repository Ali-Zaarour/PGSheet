
BEGIN;
-- SET LOCAL statement_timeout = '300s';

COPY "public"."customers" ("name", "email", "phone", "status", "credit_limit", "signup_date", "notes") FROM stdin;
Acme SARL	contact@acme.lb	+9611234567	active	15000.00	2024-03-15	\N
O'Brien & Sons	hello@obrien.ie	+35315550100	active	2500.50	2024-04-01	Pays in EUR
Zeta \\ Partners	z@zeta.example	0300000000	inactive	0.00	2023-12-31	Backslash in the name
\.

COMMIT;

-- =============================================================
--  End of script: 3 rows across 1 statement(s)
-- =============================================================
