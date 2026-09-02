// Package config loads and saves .pgsheet.json: the sheet settings, mapping,
// transforms and primary key choice for one spreadsheet layout.
//
// The file contains no connection details of any kind. These get emailed and
// committed to repositories, so they have to be safe to share.
//
// On load, a header fingerprint mismatch is reported rather than absorbed.
// That comparison is what stops a client's changed template being imported
// into the wrong columns.
package config
