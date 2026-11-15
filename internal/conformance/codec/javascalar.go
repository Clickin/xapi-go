package codec

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// The drivers convert every scalar lexical through StringTypeConverter, in
// both directions. These functions reproduce it and the Java toString forms
// of the results.

var (
	javaIntegerPattern    = regexp.MustCompile(`^[+-]?\d+$`)
	javaHexIntPattern     = regexp.MustCompile(`^[+-]?[0-9a-fA-F]+$`)
	javaBigDecimalPattern = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)
	javaDoublePattern     = regexp.MustCompile(`^[+-]?(?:NaN|Infinity|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?[fFdD]?)$`)
	javaHexDoublePattern  = regexp.MustCompile(`^[+-]?0[xX](?:[0-9a-fA-F]+\.?[0-9a-fA-F]*|\.[0-9a-fA-F]+)[pP][+-]?\d+[fFdD]?$`)
)

func removeGroupingSeparator(value string) string {
	return strings.ReplaceAll(value, ",", "")
}

// javaTrimIfSpace trims like StringTypeConverter.needTrim: only when the value
// starts or ends with a space, and then with String.trim.
func javaTrimIfSpace(value string) string {
	if value == "" || value[0] != ' ' && (len(value) == 1 || value[len(value)-1] != ' ') {
		return value
	}
	return strings.TrimFunc(value, func(r rune) bool { return r <= ' ' })
}

// javaInteger is StringTypeConverter.toInt (bits 32) and toLong (bits 64).
func javaInteger(value string, bits int) string {
	if value == "" {
		return "0"
	}
	value = removeGroupingSeparator(javaTrimIfSpace(value))
	if !strings.Contains(value, ".") {
		if bits == 32 && (strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X")) {
			hex := value[2:]
			if !javaHexIntPattern.MatchString(hex) {
				return "0"
			}
			n, err := strconv.ParseInt(hex, 16, 32)
			if err != nil {
				return "0"
			}
			return strconv.FormatInt(n, 10)
		}
		if !javaIntegerPattern.MatchString(value) {
			return "0"
		}
		n, err := strconv.ParseInt(value, 10, bits)
		if err != nil {
			return "0"
		}
		return strconv.FormatInt(n, 10)
	}
	unscaled, scale, ok := parseJavaBigDecimal(value)
	if !ok {
		return "0"
	}
	// BigDecimal.intValue/longValue: truncate toward zero, keep the low bits.
	integer := new(big.Int).Set(unscaled)
	if scale > 0 {
		integer.Quo(integer, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil))
	} else if scale < 0 {
		integer.Mul(integer, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil))
	}
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	low := new(big.Int).And(integer, mask).Uint64()
	if bits == 32 {
		return strconv.FormatInt(int64(int32(uint32(low))), 10)
	}
	return strconv.FormatInt(int64(low), 10)
}

// parseJavaBigDecimal parses the BigDecimal(String) grammar into an unscaled
// value and a scale.
func parseJavaBigDecimal(value string) (*big.Int, int, bool) {
	if !javaBigDecimalPattern.MatchString(value) {
		return nil, 0, false
	}
	mantissa, exponent := value, 0
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		e, err := strconv.Atoi(strings.TrimPrefix(value[i+1:], "+"))
		if err != nil {
			return nil, 0, false
		}
		mantissa, exponent = value[:i], e
	}
	integer, fraction, _ := strings.Cut(mantissa, ".")
	unscaled, ok := new(big.Int).SetString(integer+fraction, 10)
	if !ok {
		return nil, 0, false
	}
	return unscaled, len(fraction) - exponent, true
}

// javaBigDecimal is StringTypeConverter.toBigDecimal followed by
// BigDecimal.toString; ok is false where the driver yields null.
func javaBigDecimal(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	unscaled, scale, ok := parseJavaBigDecimal(removeGroupingSeparator(value))
	if !ok {
		return "", false
	}
	sign := ""
	if unscaled.Sign() < 0 {
		sign = "-"
	}
	digits := new(big.Int).Abs(unscaled).String()
	adjusted := -scale + len(digits) - 1
	if scale == 0 {
		return sign + digits, true
	}
	if scale > 0 && adjusted >= -6 {
		if len(digits) <= scale {
			return sign + "0." + strings.Repeat("0", scale-len(digits)) + digits, true
		}
		return sign + digits[:len(digits)-scale] + "." + digits[len(digits)-scale:], true
	}
	out := sign + digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	out += "E"
	if adjusted >= 0 {
		out += "+"
	}
	return out + strconv.Itoa(adjusted), true
}

// javaFloating is StringTypeConverter.toDouble (bits 64) or toFloat (bits 32)
// followed by Double.toString or Float.toString.
func javaFloating(value string, bits int) string {
	value = strings.TrimFunc(removeGroupingSeparator(value), func(r rune) bool { return r <= ' ' })
	var x float64
	switch {
	case javaDoublePattern.MatchString(value):
		text := strings.TrimRight(value, "fFdD")
		if strings.HasSuffix(text, "Infinity") {
			x = math.Inf(1)
			if strings.HasPrefix(text, "-") {
				x = math.Inf(-1)
			}
		} else if strings.HasSuffix(text, "NaN") {
			x = math.NaN()
		} else {
			parsed, err := strconv.ParseFloat(text, bits)
			if err != nil && !isRangeError(err) {
				return javaFloatString(0, bits)
			}
			x = parsed
		}
	case javaHexDoublePattern.MatchString(value):
		parsed, err := strconv.ParseFloat(strings.TrimRight(value, "fFdD"), bits)
		if err != nil && !isRangeError(err) {
			return javaFloatString(0, bits)
		}
		x = parsed
	default:
		return javaFloatString(0, bits)
	}
	return javaFloatString(x, bits)
}

func isRangeError(err error) bool {
	numErr, ok := err.(*strconv.NumError)
	return ok && numErr.Err == strconv.ErrRange
}

// javaFloatString formats like Double.toString (bits 64) or Float.toString
// (bits 32) on JDK 19 and later, which print the shortest uniquely
// identifying digits.
func javaFloatString(x float64, bits int) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		if math.Signbit(x) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	// Shortest digits and decimal exponent: d.ddd x 10^exp.
	sci := strconv.FormatFloat(x, 'e', -1, bits)
	mantissa, expText, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mantissa, ".", "", 1)
	if x >= 1e-3 && x < 1e7 {
		if exp >= 0 {
			if len(digits) <= exp+1 {
				return sign + digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
			}
			return sign + digits[:exp+1] + "." + digits[exp+1:]
		}
		return sign + "0." + strings.Repeat("0", -exp-1) + digits
	}
	out := sign + digits[:1] + "."
	if len(digits) > 1 {
		out += digits[1:]
	} else {
		out += "0"
	}
	return out + "E" + strconv.Itoa(exp)
}

// javaDate is StringTypeConverter.toDate/toTime/toDateTime: the length picks
// the layout (17 yyyyMMddHHmmssSSS, 14 yyyyMMddHHmmss, 8 yyyyMMdd,
// 9 HHmmssSSS, 6 HHmmss; anything else is null), each field is read as
// (char - '0') arithmetic, and a lenient calendar normalizes overflow. DATE
// drops the time, TIME moves to 1970-01-01. A time-only lexical read as DATE
// or DATETIME lands on the driver's current date, which is not reproducible;
// 1970-01-01 is used. Times are UTC.
func javaDate(value, dataType string) (time.Time, bool) {
	units := utf16.Encode([]rune(value))
	field := func(offset, length int) int {
		n := 0
		for i := offset; i < offset+length; i++ {
			n = n*10 + int(units[i]) - '0'
		}
		return n
	}
	year, month, day, hour, minute, second, millis := 1970, 1, 1, 0, 0, 0, 0
	switch len(units) {
	case 17:
		year, month, day = field(0, 4), field(4, 2), field(6, 2)
		hour, minute, second, millis = field(8, 2), field(10, 2), field(12, 2), field(14, 3)
	case 14:
		year, month, day = field(0, 4), field(4, 2), field(6, 2)
		hour, minute, second = field(8, 2), field(10, 2), field(12, 2)
	case 8:
		year, month, day = field(0, 4), field(4, 2), field(6, 2)
	case 9:
		hour, minute, second, millis = field(0, 2), field(2, 2), field(4, 2), field(6, 3)
	case 6:
		hour, minute, second = field(0, 2), field(2, 2), field(4, 2)
	default:
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, millis*int(time.Millisecond), time.UTC)
	switch strings.ToUpper(dataType) {
	case "DATE":
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "TIME":
		t = time.Date(1970, 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	}
	return t, true
}

// formatJavaDate formats like DatetimeFormat for the type: the year is
// padded to four digits and never cut.
func formatJavaDate(t time.Time, dataType string) string {
	date := fmt.Sprintf("%04d%02d%02d", t.Year(), int(t.Month()), t.Day())
	clock := fmt.Sprintf("%02d%02d%02d%03d", t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/int(time.Millisecond))
	switch strings.ToUpper(dataType) {
	case "DATE":
		return date
	case "TIME":
		return clock
	default:
		return date + clock
	}
}

func javaDateLexical(value, dataType string) (string, bool) {
	t, ok := javaDate(value, dataType)
	if !ok {
		return "", false
	}
	return formatJavaDate(t, dataType), true
}
