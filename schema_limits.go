package xapi

import (
	"fmt"
	"reflect"
)

func enforceSchemaLimits(s *schema, o options) error {
	if o.limits.MaxColumns > 0 || o.exactLimits {
		for _, d := range s.datasets {
			if limitCountExceeded(len(d.fields), o.limits.MaxColumns, o.exactLimits) {
				return fmt.Errorf("xapi: column limit exceeded for %s", d.name)
			}
		}
	}
	if o.strict {
		for _, f := range s.params {
			if isPrecisionLossField(s.typ.FieldByIndex(f.index).Type, f.typ) {
				return fmt.Errorf("xapi: %s cannot bind %s to floating-point field in strict mode", f.name, f.typ)
			}
		}
		for _, d := range s.datasets {
			for _, f := range d.fields {
				if isPrecisionLossField(d.elem.FieldByIndex(f.index).Type, f.typ) {
					return fmt.Errorf("xapi: %s.%s cannot bind %s to floating-point field in strict mode", d.name, f.name, f.typ)
				}
			}
		}
	}
	return nil
}

func isPrecisionLossField(t reflect.Type, typ string) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isOptionalType(t) {
		v, _ := t.FieldByName("Value")
		t = v.Type
	}
	return (typ == "DECIMAL" || typ == "BIGDECIMAL") && (t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64)
}
func enforceValueLimits(root reflect.Value, s *schema, o options) error {
	for root.Kind() == reflect.Pointer {
		root = root.Elem()
	}
	if limitCountExceeded(len(s.datasets), o.limits.MaxDatasets, o.exactLimits) {
		return fmt.Errorf("xapi: dataset limit exceeded")
	}
	for _, f := range s.params {
		if err := enforceScalarLimit(root.FieldByIndex(f.index), f.typ, f.name, o); err != nil {
			return err
		}
	}
	for _, d := range s.datasets {
		rows := root.FieldByIndex(d.index)
		if limitCountExceeded(rows.Len(), o.limits.MaxRows, o.exactLimits) {
			return fmt.Errorf("xapi: row limit exceeded for %s", d.name)
		}
		for i := range rows.Len() {
			rv := rows.Index(i)
			current := rv
			var original reflect.Value
			if d.tracked {
				current = rv.FieldByName("Current")
				original = rv.FieldByName("Original")
				if o.limits.MaxDepth > 0 && !original.IsNil() && o.limits.MaxDepth <= 1 {
					return fmt.Errorf("xapi: XML/JSON depth limit exceeded")
				}
			} else if d.orgRowIndex != nil {
				original = rv.FieldByIndex(d.orgRowIndex)
				if o.limits.MaxDepth > 0 && !original.IsNil() && o.limits.MaxDepth <= 1 {
					return fmt.Errorf("xapi: XML/JSON depth limit exceeded")
				}
			}
			for _, f := range d.fields {
				if err := enforceScalarLimit(current.FieldByIndex(f.index), f.typ, fmt.Sprintf("%s[%d].%s", d.name, i, f.name), o); err != nil {
					return err
				}
				if original.IsValid() && !original.IsNil() {
					if err := enforceScalarLimit(original.Elem().FieldByIndex(f.index), f.typ, fmt.Sprintf("%s[%d].OrgRow.%s", d.name, i, f.name), o); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func enforceScalarLimit(v reflect.Value, typ, path string, o options) error {
	value, missing, null := optionalValueState(v)
	if missing || null || !value.IsValid() {
		return nil
	}
	if typ == "BLOB" {
		if value.Kind() == reflect.Slice && limitBytesExceeded(int64(value.Len()), o.limits.MaxBlobBytes, o.exactLimits) {
			return fmt.Errorf("xapi: blob limit exceeded for %s", path)
		}
		return nil
	}
	if o.limits.MaxScalarBytes <= 0 && !o.exactLimits {
		return nil
	}
	lexical, err := encodeScalar(value.Interface(), typ)
	if err != nil {
		return err
	}
	if limitBytesExceeded(int64(len(lexical)), o.limits.MaxScalarBytes, o.exactLimits) {
		return fmt.Errorf("xapi: scalar limit exceeded for %s", path)
	}
	return nil
}
func limitCountExceeded(n, max int, exact bool) bool {
	return n > max && (max > 0 || exact)
}

func limitBytesExceeded(n, max int64, exact bool) bool {
	return n > max && (max > 0 || exact)
}
