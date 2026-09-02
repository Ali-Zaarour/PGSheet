package dbconn

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ServerInfo is what the session knows about the server it is talking to.
//
// StandardConformingStrings is here because the SQL generator needs it and
// must not assume it: it is on by default everywhere supported, but a file
// generated under the wrong assumption is a file that silently means something
// else (spec §5, §11).
type ServerInfo struct {
	Database                  string `json:"database"`
	User                      string `json:"user"`
	Version                   string `json:"version"`
	VersionNum                int    `json:"versionNum"`
	StandardConformingStrings bool   `json:"standardConformingStrings"`
	ServerTimezone            string `json:"serverTimezone"`
}

// Privileges are the rights the connected user has on one table.
//
// SELECT is required for uniqueness checks and the dry run. INSERT is required
// only for the dry run and direct execute; without it those buttons are
// disabled rather than failing at the moment the operator presses them.
type Privileges struct {
	Table     string `json:"table"`
	CanSelect bool   `json:"canSelect"`
	CanInsert bool   `json:"canInsert"`
}

// Probe reads the server facts the session depends on.
func Probe(ctx context.Context, pool *pgxpool.Pool) (ServerInfo, error) {
	var (
		info ServerInfo
		scs  string
	)

	// current_database() and current_user return name, not text. pgx reads
	// name happily today, but pg_catalog's string-ish types are a known source
	// of scan failures ("char" cannot be read into a string at all), so every
	// one of them is cast explicitly rather than relied upon.
	err := pool.QueryRow(ctx, `
		SELECT current_database()::text,
		       current_user::text,
		       version()::text,
		       current_setting('server_version_num')::int,
		       current_setting('standard_conforming_strings')::text,
		       current_setting('TimeZone')::text`).
		Scan(&info.Database, &info.User, &info.Version, &info.VersionNum, &scs, &info.ServerTimezone)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("probe server: %w", err)
	}

	info.StandardConformingStrings = scs == "on"
	return info, nil
}

// CheckPrivileges reports what the connected user may do with one table.
func CheckPrivileges(ctx context.Context, pool *pgxpool.Pool, schema, table string) (Privileges, error) {
	qualified := schema + "." + table

	// format('%I.%I', ...) quotes each identifier on the server side, so a
	// mixed-case or awkwardly named table resolves correctly and nothing the
	// operator typed is ever interpreted as SQL.
	p := Privileges{Table: qualified}
	err := pool.QueryRow(ctx, `
		SELECT has_table_privilege(format('%I.%I', $1::text, $2::text)::regclass, 'SELECT'),
		       has_table_privilege(format('%I.%I', $1::text, $2::text)::regclass, 'INSERT')`,
		schema, table).
		Scan(&p.CanSelect, &p.CanInsert)
	if err != nil {
		return Privileges{}, fmt.Errorf("check privileges on %s: %w", qualified, err)
	}
	return p, nil
}
