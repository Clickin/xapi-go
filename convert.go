package xapi

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func assign(dst reflect.Value, s string, typ string) error {
	return assignLimited(dst, s, typ, defaultLimits.MaxBlobBytes)
}
func assignLimited(dst reflect.Value, s string, typ string, maxBlob int64) error {
	return assignLimitedWithWhitespace(dst, s, typ, maxBlob, false)
}
func assignLimitedWithWhitespace(dst reflect.Value, s string, typ string, maxBlob int64, allowWhitespace bool) error {
	return assignLimitedMode(dst, s, typ, maxBlob, allowWhitespace, true)
}
func assignCompatible(dst reflect.Value, s string, typ string, maxBlob int64, allowWhitespace bool) error {
	return assignLimitedMode(dst, s, typ, maxBlob, allowWhitespace, false)
}
func assignLimitedMode(dst reflect.Value, s string, typ string, maxBlob int64, allowWhitespace, validateDates bool) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assignLimitedMode(dst.Elem(), s, typ, maxBlob, allowWhitespace, validateDates)
	}
	if isOptionalValue(dst) {
		dst.FieldByName("Present").SetBool(true)
		dst.FieldByName("Null").SetBool(false)
		return assignLimitedMode(dst.FieldByName("Value"), s, typ, maxBlob, allowWhitespace, validateDates)
	}
	if dst.CanAddr() {
		if u, ok := dst.Addr().Interface().(interface{ UnmarshalText([]byte) error }); ok {
			return u.UnmarshalText([]byte(s))
		}
	}
	if typ == "BLOB" {
		if dst.Kind() != reflect.Slice || dst.Type().Elem().Kind() != reflect.Uint8 {
			return &TypeError{Want: "[]byte", Got: dst.Type().String()}
		}
		b, e := decodeBlobString(s, maxBlob, allowWhitespace)
		if e != nil {
			return e
		}
		dst.SetBytes(b)
		return nil
	}
	if validateDates && typ == "DATE" {
		if len(s) != 8 {
			return fmt.Errorf("invalid DATE %q", s)
		}
		if _, e := time.Parse("20060102", s); e != nil {
			return e
		}
	}
	if validateDates && typ == "TIME" {
		if len(s) != 6 && len(s) != 9 {
			return fmt.Errorf("invalid TIME %q", s)
		}
		if _, e := time.Parse("150405", s[:6]); e != nil {
			return e
		}
		if len(s) == 9 {
			if _, e := time.Parse("150405.000", s[:6]+"."+s[6:]); e != nil {
				return e
			}
		}
	}
	if validateDates && typ == "DATETIME" {
		if len(s) != 14 && len(s) != 17 {
			return fmt.Errorf("invalid DATETIME %q", s)
		}
		if _, e := time.Parse("20060102150405", s[:14]); e != nil {
			return e
		}
		if len(s) == 17 {
			if _, e := time.Parse("20060102150405.000", s[:14]+"."+s[14:]); e != nil {
				return e
			}
		}
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(s)
	case reflect.Bool:
		v, e := strconv.ParseBool(strings.ToLower(s))
		if e != nil {
			if s == "0" {
				v = false
			} else if s == "1" {
				v = true
			} else {
				return e
			}
		}
		dst.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, e := strconv.ParseInt(s, 10, dst.Type().Bits())
		if e != nil {
			return e
		}
		dst.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, e := strconv.ParseUint(s, 10, dst.Type().Bits())
		if e != nil {
			return e
		}
		dst.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, e := strconv.ParseFloat(s, dst.Type().Bits())
		if e != nil {
			return e
		}
		dst.SetFloat(v)
	default:
		return &TypeError{Want: typ, Got: dst.Type().String()}
	}
	return nil
}

func assignNull(dst reflect.Value) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assignNull(dst.Elem())
	}
	if isOptionalValue(dst) {
		dst.FieldByName("Present").SetBool(true)
		dst.FieldByName("Null").SetBool(true)
		return nil
	}
	return nil
}
func assignJSONNull(dst reflect.Value, p JSONNullPolicy) error {
	v := dst
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if p == ErrorOnJSONNull && !isOptionalValue(v) {
		return fmt.Errorf("JSON null cannot be assigned to %s", dst.Type())
	}
	return assignNull(dst)
}
func assignBlobBytes(dst reflect.Value, b []byte) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assignBlobBytes(dst.Elem(), b)
	}
	if isOptionalValue(dst) {
		dst.FieldByName("Present").SetBool(true)
		dst.FieldByName("Null").SetBool(false)
		return assignBlobBytes(dst.FieldByName("Value"), b)
	}
	if dst.Kind() != reflect.Slice || dst.Type().Elem().Kind() != reflect.Uint8 {
		return &TypeError{Want: "[]byte", Got: dst.Type().String()}
	}
	dst.SetBytes(b)
	return nil
}
func isOptionalValue(v reflect.Value) bool {
	return v.Kind() == reflect.Struct && v.NumField() == 3 && v.FieldByName("Present").IsValid() && v.FieldByName("Null").IsValid() && v.FieldByName("Value").IsValid()
}
func assignWithOptions(dst reflect.Value, s, typ string, maxBlob int64, o options) error {
	if o.compatibility {
		return assignCompatible(dst, s, typ, maxBlob, o.base64WS)
	}
	return assignLimitedWithWhitespace(dst, s, typ, maxBlob, o.base64WS)
}
