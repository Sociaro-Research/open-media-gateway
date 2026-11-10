package catalogsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strings"
)

// Catalogue snapshots must retain interface-valued defaults exactly, including
// integers beyond float64's range. This also covers repository State/Versions reads.
func (s *State) UnmarshalJSON(data []byte) error {
	type plain State
	return decodeJSON(data, (*plain)(s))
}

func decodeJSON(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON content")
	}
	return nil
}

// Compare the persisted contract, not Go's dynamic numeric/container types.
// Failed serialization is never evidence that two contracts are unchanged.
func sameContract(a, b any) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && sameJSON(x, y)
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	return decodeJSON(a, &x) == nil && decodeJSON(b, &y) == nil && sameJSONValue(x, y)
}

func sameJSONValue(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		return ok && decimalValue(x) == decimalValue(y)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSONValue(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, value := range x {
			other, exists := y[key]
			if !exists || !sameJSONValue(value, other) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Normalize a validated JSON decimal as coefficient * 10^exponent. Keeping the
// exponent symbolic avoids rounding and allocation proportional to its value.
func decimalValue(n json.Number) string {
	value := strings.ToLower(n.String())
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	mantissa, exponent, _ := strings.Cut(value, "e")
	var power big.Int
	if exponent != "" {
		power.SetString(exponent, 10)
	}
	if whole, fraction, ok := strings.Cut(mantissa, "."); ok {
		mantissa = whole + fraction
		power.Sub(&power, big.NewInt(int64(len(fraction))))
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0"
	}
	digits := strings.TrimRight(mantissa, "0")
	power.Add(&power, big.NewInt(int64(len(mantissa)-len(digits))))
	if negative {
		digits = "-" + digits
	}
	return digits + "e" + power.String()
}
