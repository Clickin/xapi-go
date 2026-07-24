package xapi

import "reflect"

func appendReflect(dst, value reflect.Value) {
	if dst.Len() == dst.Cap() {
		capn := dst.Cap() * 2
		if capn < 64 {
			capn = 64
		}
		grown := reflect.MakeSlice(dst.Type(), dst.Len(), capn)
		reflect.Copy(grown, dst)
		dst.Set(grown)
	}
	n := dst.Len()
	dst.SetLen(n + 1)
	dst.Index(n).Set(value)
}

func optionalValueState(v reflect.Value) (value reflect.Value, missing, null bool) {
	for v.IsValid() {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, true, false
			}
			v = v.Elem()
			continue
		}
		if isOptionalType(v.Type()) {
			if !v.FieldByName("Present").Bool() {
				return reflect.Value{}, true, false
			}
			null = v.FieldByName("Null").Bool()
			v = v.FieldByName("Value")
			continue
		}
		return v, false, null
	}
	return reflect.Value{}, true, null
}

func missingValue(v reflect.Value) bool {
	if _, missing, _ := optionalValueState(v); missing {
		return true
	}
	probe := v
	for probe.IsValid() && probe.Kind() == reflect.Pointer {
		if probe.IsNil() {
			return true
		}
		probe = probe.Elem()
	}
	if probe.IsValid() && isOptionalType(probe.Type()) {
		return false
	}
	if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Map || v.Kind() == reflect.Interface) {
		return v.IsNil()
	}
	return false
}

func optionalField(v reflect.Value) bool {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return isOptionalType(v.Type().Elem())
		}
		v = v.Elem()
	}
	return v.IsValid() && isOptionalType(v.Type())
}
