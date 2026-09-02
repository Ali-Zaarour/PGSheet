// Package excel reads .xlsx workbooks: a streaming reader, header detection
// and fingerprinting, and normalization of raw cells.
//
// Two rules make the difference between correct and quietly wrong:
//
//   - Stream. f.Rows(), never GetRows(), which materializes the whole sheet.
//   - Read stored values, not formatted text. Formatting turns "00123" into
//     123 and renders a date however the workbook's locale feels.
//
// Numbers become decimal.Decimal parsed from the raw string. float64 is not
// used as an intermediate anywhere.
package excel
