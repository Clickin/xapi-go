package codec

import "github.com/Clickin/xapi-go/internal/conformance/protocol"

// Exported driver behavior for the public xapi encoders. This file is not
// part of the reference copy; keep it when the other files are synced from
// xapi-conformance.

// EscapeXML escapes text and attribute values as the original drivers do.
func EscapeXML(value string, xplatform bool) (string, error) {
	return escapeXML(value, xplatform)
}

// WireType is the type name the drivers write for a canonical type.
func WireType(dataType string) string { return wireType(dataType) }

// DefaultWireSize is the column size the drivers write when none is set.
func DefaultWireSize(dataType string) string { return defaultWireSize(dataType) }

// NormalizeScalar converts a cell lexical as the drivers' StringTypeConverter
// does; an invalid DATE/TIME/DATETIME or BIGDECIMAL lexical becomes null.
func NormalizeScalar(dataType, state, lexical string) (string, string) {
	cell := normalizeSourceCell(dataType, protocol.Cell{State: state, Lexical: lexical})
	return cell.State, cell.Lexical
}

// NormalizeConstScalar converts a constant column value: only BOOLEAN
// changes (to 1 or 0); other values are written as given.
func NormalizeConstScalar(dataType, state, lexical string) (string, string) {
	cell := normalizeSourceConstCell(dataType, protocol.Cell{State: state, Lexical: lexical})
	return cell.State, cell.Lexical
}

// ParameterText is the text the drivers write for a parameter, and whether
// they write any.
func ParameterText(dataType, state, lexical string) (string, bool) {
	return jsonScalar(dataType, protocol.Cell{State: state, Lexical: lexical})
}
