// Package mapper links spreadsheet columns to table columns and applies the
// per-column transforms.
//
// Transform order is fixed, because the results differ: strip digits, trim,
// blank-as-null, default-on-blank, case, value map, boolean map, date format.
//
// Both the pre- and post-transform values are kept, so an error can quote the
// cell as the operator sees it in Excel.
package mapper
