package validator

import (
	"fmt"
	"strconv"

	"github.com/xuri/excelize/v2"
)

// WriteXLSX exports the issue list as a workbook (spec §9).
//
// The report goes back to whoever supplied the data, and they work in Excel.
// A CSV opens there too, but a workbook keeps the row numbers numeric, freezes
// the header, and sizes the columns — which is the difference between a list
// someone works through and one they have to reformat first.
func (r *Report) WriteXLSX(path string) error {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Issues"
	index, err := f.NewSheet(sheet)
	if err != nil {
		return fmt.Errorf("build issue report: %w", err)
	}
	f.SetActiveSheet(index)
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return fmt.Errorf("build issue report: %w", err)
	}

	headers := []string{
		"Severity", "Code", "Excel row", "Excel cell", "Sheet column",
		"Table column", "Value", "Problem", "Suggested fix",
	}
	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return err
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			return err
		}
	}

	header, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#F1F5F9"}},
	})
	if err != nil {
		return err
	}
	if err := f.SetCellStyle(sheet, "A1", "I1", header); err != nil {
		return err
	}
	if err := f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, Split: false, XSplit: 0, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
	}); err != nil {
		return err
	}

	for i, issue := range r.Issues {
		row := i + 2

		text := []struct {
			col   int
			value string
		}{
			{1, string(issue.Severity)},
			{2, issue.Code},
			{4, issue.ExcelRef},
			{5, issue.ExcelColumn},
			{6, issue.DBColumn},
			// The offending value is written as a string on purpose: writing it
			// as a number would let Excel reformat the very value the report is
			// complaining about.
			{7, issue.Value},
			{8, issue.Message},
			{9, issue.Hint},
		}
		for _, t := range text {
			cell, err := excelize.CoordinatesToCellName(t.col, row)
			if err != nil {
				return err
			}
			if err := f.SetCellStr(sheet, cell, t.value); err != nil {
				return err
			}
		}

		if issue.ExcelRow > 0 {
			cell, err := excelize.CoordinatesToCellName(3, row)
			if err != nil {
				return err
			}
			// Numeric, so the recipient can sort and filter by row.
			if err := f.SetCellInt(sheet, cell, issue.ExcelRow); err != nil {
				return err
			}
		}
	}

	widths := map[string]float64{
		"A": 10, "B": 8, "C": 10, "D": 11, "E": 22, "F": 22, "G": 30, "H": 60, "I": 50,
	}
	for col, w := range widths {
		if err := f.SetColWidth(sheet, col, col, w); err != nil {
			return err
		}
	}

	if len(r.Issues) > 0 {
		last := "I" + strconv.Itoa(len(r.Issues)+1)
		if err := f.AutoFilter(sheet, "A1:"+last, nil); err != nil {
			return err
		}
	}

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("write issue report: %w", err)
	}
	return nil
}
