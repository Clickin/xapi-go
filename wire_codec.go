package xapi

import (
	"encoding/base64"
	"fmt"
	"reflect"
)

type wireCell struct {
	state   string
	lexical string
}
type wireParameter struct {
	id, typ, size string
	cell          wireCell
}
type wireColumn struct {
	id, typ, size, prop, sum string
	constant                 bool
	cell                     wireCell
}
type wireRow struct {
	typ      RowType
	values   map[string]wireCell
	original *wireRow
}
type wireDataset struct {
	id, alias string
	constants []wireColumn
	columns   []wireColumn
	rows      []wireRow
}
type wireDocument struct {
	parameters []wireParameter
	datasets   []wireDataset
}

func wireDocumentFromValue(src any, o options) (wireDocument, error) {
	v := reflect.ValueOf(src)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return wireDocument{}, fmt.Errorf("xapi: nil source")
		}
		v = v.Elem()
	}
	s, err := compileSchema(v.Type())
	if err != nil {
		return wireDocument{}, err
	}
	if err = enforceSchemaLimits(s, o); err != nil {
		return wireDocument{}, err
	}
	if err = enforceValueLimits(v, s, o); err != nil {
		return wireDocument{}, err
	}
	doc := wireDocument{}
	for _, f := range s.params {
		cell, err := wireCellFromValue(v.FieldByIndex(f.index), f.typ, o)
		if err != nil {
			return wireDocument{}, &PathError{Path: f.name, Err: err}
		}
		if cell.state != "missing" {
			doc.parameters = append(doc.parameters, wireParameter{id: f.name, typ: f.typ, size: intString(f.size), cell: cell})
		}
	}
	for _, sd := range s.datasets {
		wd := wireDataset{id: sd.name, alias: sd.name}
		zero := reflect.New(sd.elem).Elem()
		for _, f := range sd.fields {
			col := wireColumn{id: f.name, typ: f.typ, size: intString(f.size), constant: f.constant}
			if f.constant {
				if f.hasConstValue {
					col.cell = wireCell{state: "value", lexical: f.constValue}
				} else {
					col.cell, err = wireCellFromValue(zero.FieldByIndex(f.index), f.typ, o)
				}
				if err != nil {
					return wireDocument{}, &PathError{Path: wd.id + "." + f.name, Err: err}
				}
				wd.constants = append(wd.constants, col)
			} else {
				wd.columns = append(wd.columns, col)
			}
		}
		rv := v.FieldByIndex(sd.index)
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i)
			if item.Kind() == reflect.Pointer {
				if item.IsNil() {
					return wireDocument{}, fmt.Errorf("xapi: nil row pointer")
				}
				item = item.Elem()
			}
			row := reflectRow(item, sd, o)
			if row.err != nil {
				return wireDocument{}, &PathError{Path: fmt.Sprintf("%s[%d]", wd.id, i), Err: row.err}
			}
			wd.rows = append(wd.rows, row.value)
		}
		doc.datasets = append(doc.datasets, wd)
	}
	return doc, nil
}

type reflectedRow struct {
	value wireRow
	err   error
}

func reflectRow(v reflect.Value, d dataset, o options) reflectedRow {
	row := wireRow{typ: RowNormal, values: map[string]wireCell{}}
	if d.tracked {
		row.typ = RowType(v.FieldByName("Type").Uint())
		current := v.FieldByName("Current")
		if original := v.FieldByName("Original"); original.IsValid() && !original.IsNil() {
			r := reflectRowValues(original.Elem(), d, o)
			if r.err != nil {
				return reflectedRow{err: r.err}
			}
			row.original = &r.value
		}
		values := reflectRowValues(current, d, o)
		return reflectedRow{value: wireRow{typ: row.typ, values: values.value.values, original: row.original}, err: values.err}
	}
	if d.rowTypeIndex != nil {
		row.typ = RowType(v.FieldByIndex(d.rowTypeIndex).Uint())
	}
	values := reflectRowValues(v, d, o)
	row.values = values.value.values
	if d.orgRowIndex != nil {
		p := v.FieldByIndex(d.orgRowIndex)
		if !p.IsNil() {
			r := reflectRowValues(p.Elem(), d, o)
			if r.err != nil {
				return reflectedRow{err: r.err}
			}
			row.original = &r.value
		}
	}
	return reflectedRow{value: row, err: values.err}
}

func reflectRowValues(v reflect.Value, d dataset, o options) reflectedRow {
	row := wireRow{typ: RowNormal, values: map[string]wireCell{}}
	for _, f := range d.fields {
		if f.constant {
			continue
		}
		cell, err := wireCellFromValue(v.FieldByIndex(f.index), f.typ, o)
		if err != nil {
			return reflectedRow{err: fmt.Errorf("%s: %w", f.name, err)}
		}
		if cell.state != "missing" {
			row.values[f.name] = cell
		}
	}
	return reflectedRow{value: row}
}

func wireCellFromValue(v reflect.Value, typ string, o options) (wireCell, error) {
	if unwrapped, missing, null := optionalValueState(v); missing {
		return wireCell{state: "missing"}, nil
	} else if null {
		return wireCell{state: "null"}, nil
	} else if unwrapped.IsValid() {
		v = unwrapped
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return wireCell{state: "missing"}, nil
		}
		v = v.Elem()
	}
	ss, err := encodeScalar(v.Interface(), typ)
	if err != nil {
		return wireCell{}, err
	}
	if ss == "" {
		return wireCell{state: "empty"}, nil
	}
	return wireCell{state: "value", lexical: ss}, nil
}

func applyWireDocument(dst any, doc wireDocument, o options) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("xapi: destination must be pointer to struct")
	}
	root := v.Elem()
	s, err := compileSchema(root.Type())
	if err != nil {
		return err
	}
	if err = enforceSchemaLimits(s, o); err != nil {
		return err
	}
	for _, p := range doc.parameters {
		for _, f := range s.params {
			if f.name == p.id {
				if err := assignWireCell(root.FieldByIndex(f.index), p.cell, f.typ, o); err != nil {
					return &PathError{Path: p.id, Err: err}
				}
			}
		}
	}
	for _, wd := range doc.datasets {
		var d *dataset
		for i := range s.datasets {
			if s.datasets[i].name == wd.id || (wd.alias != "" && s.datasets[i].name == wd.alias) {
				d = &s.datasets[i]
				break
			}
		}
		if d == nil {
			if o.unknown == ErrorUnknownColumns || o.strict {
				return fmt.Errorf("xapi: unknown dataset %q", wd.id)
			}
			continue
		}
		for _, col := range wd.columns {
			if _, ok := d.lookup[col.id]; !ok && o.unknown == ErrorUnknownColumns {
				return fmt.Errorf("xapi: unknown column %s.%s", wd.id, col.id)
			}
		}
		target := root.FieldByIndex(d.index)
		for i, wr := range wd.rows {
			rv := reflect.New(d.elem).Elem()
			for _, cf := range d.fields {
				if cf.constant {
					if err := assignWireCell(rv.FieldByIndex(cf.index), findColumnCell(wd, cf.name), cf.typ, o); err != nil {
						return &PathError{Path: fmt.Sprintf("%s[%d].%s", wd.id, i, cf.name), Err: err}
					}
				}
			}
			for _, cf := range d.fields {
				if cf.constant {
					continue
				}
				if err := assignWireCell(rv.FieldByIndex(cf.index), wr.values[cf.name], cf.typ, o); err != nil {
					return &PathError{Path: fmt.Sprintf("%s[%d].%s", wd.id, i, cf.name), Err: err}
				}
			}
			if d.rowTypeIndex != nil {
				rv.FieldByIndex(d.rowTypeIndex).SetUint(uint64(wr.typ))
			}
			if d.orgRowIndex != nil && wr.original != nil {
				org := reflect.New(d.elem)
				if err := applyWireRow(org.Elem(), *wr.original, *d, o); err != nil {
					return err
				}
				rv.FieldByIndex(d.orgRowIndex).Set(org)
			}
			if d.tracked {
				tr := reflect.New(target.Type().Elem()).Elem()
				tr.FieldByName("Type").SetUint(uint64(wr.typ))
				if err := applyWireRow(tr.FieldByName("Current"), wr, *d, o); err != nil {
					return err
				}
				if wr.original != nil {
					applyWireConstants(tr.FieldByName("Current"), wd, *d, o)
					org := reflect.New(d.elem)
					applyWireConstants(org.Elem(), wd, *d, o)
					if err := applyWireRow(org.Elem(), *wr.original, *d, o); err != nil {
						return err
					}
					tr.FieldByName("Original").Set(org)
				}
				target.Set(reflect.Append(target, tr))
			} else {
				target.Set(reflect.Append(target, rv))
			}
		}
	}
	return nil
}
func applyWireConstants(dst reflect.Value, wd wireDataset, d dataset, o options) {
	for _, f := range d.fields {
		if !f.constant {
			continue
		}
		_ = assignWireCell(dst.FieldByIndex(f.index), findColumnCell(wd, f.name), f.typ, o)
	}
}

func applyWireRow(dst reflect.Value, row wireRow, d dataset, o options) error {
	for _, f := range d.fields {
		if f.constant {
			continue
		}
		if err := assignWireCell(dst.FieldByIndex(f.index), row.values[f.name], f.typ, o); err != nil {
			return err
		}
	}
	return nil
}

func findColumnCell(d wireDataset, id string) wireCell {
	for _, c := range d.constants {
		if c.id == id {
			return c.cell
		}
	}
	return wireCell{state: "missing"}
}
func assignWireCell(dst reflect.Value, cell wireCell, typ string, o options) error {
	switch cell.state {
	case "missing":
		return nil
	case "null":
		return assignNull(dst)
	case "empty":
		return assignWithOptions(dst, "", typ, o.limits.MaxBlobBytes, o)
	case "value":
		return assignWithOptions(dst, cell.lexical, typ, o.limits.MaxBlobBytes, o)
	default:
		return fmt.Errorf("xapi: invalid wire cell state %q", cell.state)
	}
}

func intString(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func decodeWireBlob(lexical string, max int64, allowWhitespace bool) ([]byte, error) {
	return decodeBlobString(lexical, max, allowWhitespace)
}

func encodeWireBlob(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
func enforceWireDocumentLimits(doc wireDocument, o options) error {
	if o.limits.MaxDatasets > 0 && len(doc.datasets) > o.limits.MaxDatasets {
		return fmt.Errorf("xapi: dataset limit exceeded")
	}
	rows := 0
	columns := 0
	for _, d := range doc.datasets {
		columns += len(d.columns) + len(d.constants)
		rows += len(d.rows)
		for _, r := range d.rows {
			if r.original != nil {
				rows++
			}
		}
	}
	if o.limits.MaxRows > 0 && rows > o.limits.MaxRows {
		return fmt.Errorf("xapi: row limit exceeded")
	}
	if o.limits.MaxColumns > 0 && columns > o.limits.MaxColumns {
		return fmt.Errorf("xapi: column limit exceeded")
	}
	return nil
}
