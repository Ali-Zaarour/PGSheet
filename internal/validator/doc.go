// Package validator checks the mapped spreadsheet against the table's real
// rules and produces the report.
//
// Two phases, because two kinds of problem need different answers. Phase A
// judges columns from a sample: a column whose values mostly fail its target
// type means the wrong column was mapped, and saying that once beats repeating
// it nine hundred times. Phase B judges every cell, and only runs if Phase A
// found nothing blocking.
//
// CHECK constraints are parsed as a deliberate subset. Anything outside it is
// reported as unverifiable rather than guessed at.
//
// dryrun.go must never commit; execute.go is the only file that does.
package validator
