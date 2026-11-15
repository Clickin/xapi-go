package codec

import (
	"regexp"
	"strings"

	"github.com/Clickin/xapi-go/internal/conformance/protocol"
)

var sourceDecimalPattern = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)

// applyScalarCompatibility reproduces StringTypeConverter normalization before
// a canonical value reaches a commercial serializer. Decode remains lexical:
// normalization applies only to emitted wire values.
func applyScalarCompatibility(value protocol.Value) protocol.Value {
	parameters := append([]protocol.Parameter(nil), value.Parameters...)
	for i := range parameters {
		cell := normalizeSourceCell(parameters[i].Type, protocol.Cell{State: parameters[i].State, Lexical: parameters[i].Lexical})
		parameters[i].State, parameters[i].Lexical = cell.State, cell.Lexical
	}
	value.Parameters = parameters

	datasets := append([]protocol.Dataset(nil), value.Datasets...)
	for di := range datasets {
		dataset := &datasets[di]
		constants := append([]protocol.ConstColumn(nil), dataset.ConstColumns...)
		for i := range constants {
			constants[i].Value = normalizeSourceConstCell(constants[i].Type, constants[i].Value)
		}
		dataset.ConstColumns = constants

		types := make(map[string]string, len(dataset.Columns))
		for _, column := range dataset.Columns {
			types[column.ID] = column.Type
		}
		rows := append([]protocol.Row(nil), dataset.Rows...)
		for i := range rows {
			rows[i].Values = normalizeSourceCells(rows[i].Values, types)
			if rows[i].OrgRow != nil {
				org := *rows[i].OrgRow
				org.Values = normalizeSourceCells(org.Values, types)
				rows[i].OrgRow = &org
			}
		}
		dataset.Rows = rows
	}
	value.Datasets = datasets
	return value
}

func normalizeSourceCells(cells map[string]protocol.Cell, types map[string]string) map[string]protocol.Cell {
	if cells == nil {
		return nil
	}
	out := make(map[string]protocol.Cell, len(cells))
	for id, cell := range cells {
		out[id] = normalizeSourceCell(types[id], cell)
	}
	return out
}

func normalizeSourceCell(dataType string, cell protocol.Cell) protocol.Cell {
	if cell.State != "value" {
		return cell
	}
	switch strings.ToUpper(defaultType(dataType)) {
	case "BOOLEAN":
		if sourceBoolean(cell.Lexical) {
			cell.Lexical = "1"
		} else {
			cell.Lexical = "0"
		}
	case "SHORT", "USHORT", "INT", "UINT":
		cell.Lexical = javaInteger(cell.Lexical, 32)
	case "LONG", "ULONG":
		cell.Lexical = javaInteger(cell.Lexical, 64)
	case "FLOAT":
		cell.Lexical = javaFloating(cell.Lexical, 32)
	case "DOUBLE":
		cell.Lexical = javaFloating(cell.Lexical, 64)
	case "DECIMAL", "BIGDECIMAL":
		decimal, ok := javaBigDecimal(cell.Lexical)
		if !ok {
			return protocol.Cell{State: "null"}
		}
		cell.Lexical = decimal
	case "DATE", "TIME", "DATETIME":
		date, ok := javaDateLexical(cell.Lexical, strings.ToUpper(defaultType(dataType)))
		if !ok {
			return protocol.Cell{State: "null"}
		}
		cell.Lexical = date
	}
	return cell
}

// normalizeSourceConstCell converts only BOOLEAN constants, which the drivers
// always write as 1 or 0 (empty and null as 0); other constant values are
// written as given.
func normalizeSourceConstCell(dataType string, cell protocol.Cell) protocol.Cell {
	if cell.State == "missing" {
		return cell
	}
	switch strings.ToUpper(defaultType(dataType)) {
	case "BOOLEAN":
		cell.State = "value"
		if sourceBoolean(cell.Lexical) {
			cell.Lexical = "1"
		} else {
			cell.Lexical = "0"
		}
	}
	return cell
}

func sourceBoolean(value string) bool {
	switch value {
	case "true", "True", "TRUE", "yes", "Yes", "YES", "y", "Y", "on", "On", "ON", "1":
		return true
	default:
		return false
	}
}

func jsonScalar(dataType string, cell protocol.Cell) (string, bool) {
	dataType = strings.ToUpper(defaultType(dataType))
	if cell.State == "empty" {
		switch dataType {
		case "STRING", "CHAR", "FILE", "BLOB":
			return "", true
		}
	}
	if cell.State != "value" {
		if dataType == "BOOLEAN" {
			return "0", true
		}
		return "", false
	}
	if cell.Lexical != "" {
		return cell.Lexical, true
	}
	switch dataType {
	case "STRING", "CHAR", "FILE", "BLOB":
		return "", true
	case "SHORT", "USHORT", "INT", "UINT", "LONG", "ULONG":
		return "0", true
	case "FLOAT", "DOUBLE":
		return "0.0", true
	case "BOOLEAN":
		return "0", true
	default:
		return "", false
	}
}
