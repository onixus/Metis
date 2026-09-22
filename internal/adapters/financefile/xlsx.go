package financefile

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/onixus/metis/internal/ports"
)

type workbook struct {
	Sheets []struct {
		Name string `xml:"name,attr"`
		ID   string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type relationship struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
	Mode   string `xml:"TargetMode,attr"`
	Type   string `xml:"Type,attr"`
}

type relationships struct {
	Items []relationship `xml:"Relationship"`
}

type textRun struct {
	Text string `xml:"t"`
}
type richText struct {
	Text string    `xml:"t"`
	Runs []textRun `xml:"r"`
}

func (r richText) value() string {
	var b strings.Builder
	b.WriteString(r.Text)
	for _, run := range r.Runs {
		b.WriteString(run.Text)
	}
	return b.String()
}

type worksheetRow struct {
	Number int `xml:"r,attr"`
	Cells  []struct {
		Ref     string   `xml:"r,attr"`
		Type    string   `xml:"t,attr"`
		Value   *string  `xml:"v"`
		Formula *string  `xml:"f"`
		Inline  richText `xml:"is"`
	} `xml:"c"`
}

func readXLSX(ctx context.Context, data []byte, template ports.FinanceTemplate) ([]sourceRow, string, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", invalid("invalid XLSX ZIP container")
	}
	entries, err := inspectZIP(z)
	if err != nil {
		return nil, "", err
	}
	var workbookRels relationships
	for name, f := range entries {
		if err := ctx.Err(); err != nil {
			return nil, "", fmt.Errorf("financefile: %w", err)
		}
		if !strings.HasSuffix(strings.ToLower(name), ".rels") {
			continue
		}
		data, err := readEntry(ctx, f)
		if err != nil {
			return nil, "", err
		}
		var rels relationships
		if err := xml.Unmarshal(data, &rels); err != nil {
			return nil, "", invalid("invalid relationships")
		}
		ids := make(map[string]bool)
		for _, r := range rels.Items {
			if r.ID == "" || ids[r.ID] {
				return nil, "", invalid("missing or duplicate relationship ID")
			}
			ids[r.ID] = true
			if strings.EqualFold(r.Mode, "External") || (r.Mode != "" && r.Mode != "Internal") || strings.Contains(r.Target, ":") || strings.HasPrefix(r.Target, "//") || strings.Contains(r.Target, "\\") {
				return nil, "", invalid("external relationships are forbidden")
			}
			if strings.Contains(strings.ToLower(r.Type), "externallink") {
				return nil, "", invalid("external links are forbidden")
			}
		}
		if name == "xl/_rels/workbook.xml.rels" {
			workbookRels = rels
		}
	}
	contentTypes, err := readEntry(ctx, entries["[Content_Types].xml"])
	if err != nil {
		return nil, "", err
	}
	lowerTypes := strings.ToLower(string(contentTypes))
	if strings.Contains(lowerTypes, "macroenabled") || strings.Contains(lowerTypes, "vbaproject") || strings.Contains(lowerTypes, "externallink") {
		return nil, "", invalid("macros and external links are forbidden")
	}
	bookData, err := readEntry(ctx, entries["xl/workbook.xml"])
	if err != nil {
		return nil, "", err
	}
	var book workbook
	if err := xml.Unmarshal(bookData, &book); err != nil {
		return nil, "", invalid("invalid workbook")
	}
	if len(book.Sheets) == 0 || len(book.Sheets) > 32 {
		return nil, "", invalid("workbook must have between 1 and 32 sheets")
	}
	sheetName, sheetID := "", ""
	seenNames := make(map[string]bool)
	for _, sheet := range book.Sheets {
		if seenNames[sheet.Name] {
			return nil, "", invalid("duplicate sheet name")
		}
		seenNames[sheet.Name] = true
		if sheet.Name == template.Sheet || (template.Sheet == "" && sheetID == "") {
			sheetName, sheetID = sheet.Name, sheet.ID
		}
	}
	if sheetID == "" {
		return nil, "", invalid("requested worksheet does not exist")
	}
	sheetPath := ""
	for _, rel := range workbookRels.Items {
		if rel.ID != sheetID {
			continue
		}
		if !strings.HasSuffix(rel.Type, "/worksheet") {
			return nil, "", invalid("requested sheet is not a worksheet")
		}
		sheetPath = path.Clean(path.Join("xl", rel.Target))
		if strings.HasPrefix(rel.Target, "/") {
			sheetPath = strings.TrimPrefix(path.Clean(rel.Target), "/")
		}
		if !strings.HasPrefix(sheetPath, "xl/worksheets/") || strings.Contains(rel.Target, "..") {
			return nil, "", invalid("invalid worksheet relationship")
		}
	}
	if sheetPath == "" {
		return nil, "", invalid("worksheet relationship is missing")
	}
	var shared []string
	if f := entries["xl/sharedStrings.xml"]; f != nil {
		data, err := readEntry(ctx, f)
		if err != nil {
			return nil, "", err
		}
		var table struct {
			Strings []richText `xml:"si"`
		}
		if err := xml.Unmarshal(data, &table); err != nil {
			return nil, "", invalid("invalid shared strings")
		}
		if len(table.Strings) > maxRows*maxColumns {
			return nil, "", invalid("shared string limit exceeded")
		}
		for _, item := range table.Strings {
			value := item.value()
			if len(value) > maxCellSize {
				return nil, "", invalid("shared string exceeds cell limit")
			}
			shared = append(shared, value)
		}
	}
	sheetData, err := readEntry(ctx, entries[sheetPath])
	if err != nil {
		return nil, "", err
	}
	rows, err := readWorksheet(ctx, sheetData, shared, template.HeaderRow)
	return rows, sheetName, err
}

func inspectZIP(z *zip.Reader) (map[string]*zip.File, error) {
	if len(z.File) > maxEntries {
		return nil, invalid("ZIP entry limit exceeded")
	}
	entries := make(map[string]*zip.File)
	seen := make(map[string]bool)
	var total uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		lower := strings.ToLower(name)
		if name == "" || name == ".." || len(name) > 256 || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || seen[lower] || f.Flags&1 != 0 {
			return nil, invalid("unsafe, encrypted, or duplicate ZIP entry")
		}
		seen[lower] = true
		if strings.Contains(lower, "vbaproject") || strings.Contains(lower, "externallinks") || strings.HasSuffix(lower, ".bin") || strings.Contains(lower, "macrosheet") {
			return nil, invalid("macros and external links are forbidden")
		}
		if f.UncompressedSize64 > maxEntrySize || f.CompressedSize64 > MaxFileSize || (f.UncompressedSize64 > 0 && (f.CompressedSize64 == 0 || f.UncompressedSize64 > f.CompressedSize64*maxCompressionRatio)) {
			return nil, invalid("ZIP size or compression ratio limit exceeded")
		}
		total += f.UncompressedSize64
		if total > maxExpandedSize {
			return nil, invalid("ZIP expanded size limit exceeded")
		}
		if !f.FileInfo().IsDir() {
			entries[f.Name] = f
		}
	}
	return entries, nil
}

func readEntry(ctx context.Context, f *zip.File) ([]byte, error) {
	if f == nil {
		return nil, invalid("required XLSX entry is missing")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("financefile: %w", err)
	}
	r, err := f.Open()
	if err != nil {
		return nil, invalid("cannot read XLSX entry")
	}
	data, readErr := io.ReadAll(io.LimitReader(r, maxEntrySize+1))
	closeErr := r.Close()
	if readErr != nil || closeErr != nil {
		return nil, invalid("invalid XLSX entry checksum or content")
	}
	if len(data) > maxEntrySize {
		return nil, invalid("XLSX entry size limit exceeded")
	}
	if err := validateXML(ctx, data); err != nil {
		return nil, err
	}
	return data, nil
}

func validateXML(ctx context.Context, data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, tokens, rows := 0, 0, 0
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return invalid("invalid XML")
		}
		tokens++
		if tokens%256 == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("financefile: %w", err)
			}
		}
		if tokens > 2000000 {
			return invalid("XML token limit exceeded")
		}
		switch t := token.(type) {
		case xml.Directive:
			return invalid("XML directives and DTD are forbidden")
		case xml.ProcInst:
			if t.Target != "xml" {
				return invalid("XML processing instructions are forbidden")
			}
		case xml.StartElement:
			depth++
			if depth > 32 || len(t.Attr) > 32 {
				return invalid("XML nesting or attribute limit exceeded")
			}
			attributes := make(map[xml.Name]bool, len(t.Attr))
			for _, attribute := range t.Attr {
				if attributes[attribute.Name] {
					return invalid("duplicate XML attribute")
				}
				attributes[attribute.Name] = true
			}
			if t.Name.Local == "row" {
				rows++
				if rows > maxRows*2 {
					return invalid("worksheet row limit exceeded")
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

func readWorksheet(ctx context.Context, data []byte, shared []string, headerRow int) ([]sourceRow, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var rows []sourceRow
	previous := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("financefile: %w", err)
		}
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, invalid("invalid worksheet XML")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		var raw worksheetRow
		if err := d.DecodeElement(&raw, &start); err != nil {
			return nil, invalid("invalid worksheet row")
		}
		if raw.Number == 0 {
			raw.Number = previous + 1
		}
		if raw.Number <= previous || raw.Number > maxRows+headerRow || len(rows) >= maxRows+headerRow || len(raw.Cells) > maxColumns {
			return nil, invalid("worksheet row order or limit exceeded")
		}
		previous = raw.Number
		row := sourceRow{number: raw.Number}
		used := make(map[int]bool)
		for i, rawCell := range raw.Cells {
			index := i
			if rawCell.Ref != "" {
				var err error
				index, err = cellColumn(rawCell.Ref, raw.Number)
				if err != nil {
					return nil, err
				}
			}
			if index >= maxColumns || used[index] {
				return nil, invalid("worksheet column limit or duplicate cell")
			}
			used[index] = true
			for len(row.cells) <= index {
				row.cells = append(row.cells, cell{})
			}
			value := ""
			if rawCell.Value != nil {
				value = *rawCell.Value
			}
			c := cell{value: value}
			switch rawCell.Type {
			case "", "n", "str":
			case "inlineStr":
				c.value = rawCell.Inline.value()
			case "s":
				index, err := strconv.Atoi(value)
				if err != nil || index < 0 || index >= len(shared) {
					c.err = "invalid shared string reference"
				} else {
					c.value = shared[index]
				}
			default:
				c.err = "unsupported or error cell type"
			}
			if rawCell.Formula != nil && rawCell.Value == nil {
				c.err = "formula has no cached value"
			}
			if rawCell.Formula != nil && rawCell.Type == "inlineStr" {
				c.err = "formula must have a cached scalar value"
			}
			if len(c.value) > maxCellSize {
				return nil, invalid("cell exceeds 4096 bytes")
			}
			row.cells[index] = c
		}
		rows = append(rows, row)
	}
}

func cellColumn(ref string, row int) (int, error) {
	column, i := 0, 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		column = column*26 + int(ref[i]-'A') + 1
		if column > maxColumns {
			return 0, invalid("worksheet column limit exceeded")
		}
		i++
	}
	n, err := strconv.Atoi(ref[i:])
	if i == 0 || err != nil || n != row {
		return 0, invalid("invalid worksheet cell reference")
	}
	return column - 1, nil
}
