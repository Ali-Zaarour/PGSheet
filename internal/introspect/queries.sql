-- PGSheet introspection queries (spec §6).
--
-- Embedded with go:embed and split on the "-- name:" markers below.
-- Every parameter is bound. Nothing in this file is ever built with string
-- formatting, and no query in it writes.
--
-- Every catalogue column is cast to text or text[] before it leaves the
-- server. pg_catalog uses two types that look like strings and are not:
-- "char" (OID 18, a single byte — attidentity, attgenerated, typtype,
-- contype) and name (OID 19). A driver reading "char" in binary format
-- cannot put it in a Go string, and the failure surfaces as an opaque
-- "cannot scan char (OID 18)" at the point of use. Casting here keeps that
-- concern in the one file that knows about the catalogue.

-- name: list_tables
-- Tables the connected user can actually read. Partitioned tables included.
SELECT n.nspname::text         AS schema,
       c.relname::text         AS table,
       c.reltuples::bigint     AS est_rows,
       obj_description(c.oid)  AS comment
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
  AND has_table_privilege(c.oid, 'SELECT')
ORDER BY n.nspname, c.relname;

-- name: columns
-- $1 = schema, $2 = table
SELECT a.attnum                                    AS ordinal_position,
       a.attname::text                             AS column_name,
       t.typname::text                             AS udt_name,
       format_type(a.atttypid, a.atttypmod)        AS formatted_type,
       NOT a.attnotnull                            AS nullable,
       pg_get_expr(d.adbin, d.adrelid)             AS default_expr,
       a.atthasdef                                 AS has_default,
       a.attidentity::text                         AS identity_kind,   -- 'a','d',''
       a.attgenerated::text                        AS generated_kind,  -- 's' or ''
       CASE WHEN t.typname IN ('varchar', 'bpchar')
            THEN information_schema._pg_char_max_length(a.atttypid, a.atttypmod)
       END                                         AS max_length,
       information_schema._pg_numeric_precision(a.atttypid, a.atttypmod) AS num_precision,
       information_schema._pg_numeric_scale(a.atttypid, a.atttypmod)     AS num_scale,
       t.typtype::text                             AS type_category,   -- 'e' = enum, 'b' = base
       tn.nspname::text                            AS type_schema,     -- qualifies the enum cast
       col_description(a.attrelid, a.attnum)       AS comment,
       et.typname::text                            AS array_elem_type
FROM pg_attribute a
JOIN pg_class c      ON c.oid = a.attrelid
JOIN pg_namespace n  ON n.oid = c.relnamespace
JOIN pg_type t       ON t.oid = a.atttypid
JOIN pg_namespace tn ON tn.oid = t.typnamespace
LEFT JOIN pg_type et ON et.oid = t.typelem AND t.typcategory = 'A'
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1
  AND c.relname = $2
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum;

-- name: constraints
-- $1 = schema, $2 = table
SELECT con.conname::text,
       con.contype::text,                          -- p,u,c,f,x
       pg_get_constraintdef(con.oid) AS definition,
       ARRAY(
         SELECT a.attname::text
         FROM unnest(con.conkey) k
         JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k
         ORDER BY array_position(con.conkey, k)
       )::text[] AS columns,
       CASE WHEN con.contype = 'f'
            THEN (SELECT (n2.nspname || '.' || c2.relname)::text
                  FROM pg_class c2
                  JOIN pg_namespace n2 ON n2.oid = c2.relnamespace
                  WHERE c2.oid = con.confrelid)
       END AS ref_table,
       CASE WHEN con.contype = 'f'
            THEN ARRAY(SELECT a.attname::text
                       FROM unnest(con.confkey) k
                       JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k)::text[]
       END AS ref_columns
FROM pg_constraint con
JOIN pg_class c     ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1
  AND c.relname = $2;

-- name: unique_indexes
-- CREATE UNIQUE INDEX leaves no pg_constraint row but is enforced identically.
-- A definition containing WHERE is a partial index: flag it as not fully
-- verifiable offline and recommend live verification.
-- $1 = schema, $2 = table
SELECT i.relname::text,
       ix.indisunique,
       ix.indisprimary,
       pg_get_indexdef(ix.indexrelid) AS definition,
       ARRAY(SELECT a.attname::text
             FROM unnest(ix.indkey) k
             JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k)::text[] AS columns
FROM pg_index ix
JOIN pg_class i     ON i.oid = ix.indexrelid
JOIN pg_class c     ON c.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1
  AND c.relname = $2
  AND ix.indisunique;

-- name: enum_values
-- $1 = type name, $2 = type schema
--
-- Qualified by schema on purpose: two schemas can each define an enum called
-- "status", and merging their labels would let a value pass validation that
-- the target column does not actually accept.
SELECT e.enumlabel::text
FROM pg_enum e
JOIN pg_type t      ON t.oid = e.enumtypid
JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE t.typname = $1
  AND n.nspname = $2
ORDER BY e.enumsortorder;

-- name: serial_sequence
-- $1 = schema, $2 = table, $3 = column
--
-- format('%I.%I', ...) quotes each identifier on the server, so a mixed-case
-- or awkwardly named table resolves instead of silently returning NULL.
SELECT pg_get_serial_sequence(format('%I.%I', $1::text, $2::text), $3::text);

-- name: sequence_state
-- Reads the sequence WITHOUT consuming a value. nextval is never called for
-- display: it would burn an id even if the operator cancels the whole run.
-- $1 = schema, $2 = sequence name
SELECT last_value,
       increment_by,
       CASE WHEN last_value IS NULL THEN start_value END AS start_value
FROM pg_sequences
WHERE schemaname = $1
  AND sequencename = $2;

-- name: probe_server
SELECT current_database(),
       current_user,
       version(),
       current_setting('server_version_num')::int,
       current_setting('standard_conforming_strings');

-- name: probe_table_privileges
-- $1 = 'schema.table'
SELECT has_table_privilege($1, 'SELECT') AS can_select,
       has_table_privilege($1, 'INSERT') AS can_insert;
