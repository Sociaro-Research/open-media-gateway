// Package money represents USD as integer millionths, avoiding floating-point
// accumulation in budgets, reservations and invoice reconciliation.
package money

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type USD int64

const Scale int64 = 1_000_000

func Parse(s string) (USD, error) {
	if len(s) > 64 {
		return 0, errors.New("USD amount is too long")
	}
	if at := strings.IndexAny(s, "eE"); at >= 0 {
		exponent, err := strconv.Atoi(s[at+1:])
		if err != nil || exponent > 20 || exponent < -20 {
			return 0, errors.New("USD exponent is out of range")
		}
	}

	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, errors.New("invalid USD amount")
	}
	r.Mul(r, big.NewRat(Scale, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, errors.New("USD amount must fit six decimal places")
	}
	return USD(r.Num().Int64()), nil
}
func (u USD) MarshalJSON() ([]byte, error) { return []byte(u.String()), nil }
func (u *USD) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || bytes.Equal(b, []byte("null")) || b[0] == '"' {
		return errors.New("USD amount must be a JSON number")
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	v, err := Parse(n.String())
	if err == nil {
		*u = v
	}
	return err
}
func (u USD) String() string {
	return new(big.Rat).SetFrac(big.NewInt(int64(u)), big.NewInt(Scale)).FloatString(6)
}
func (u USD) Float64() float64 { return float64(u) / float64(Scale) }

// Multiply rounds up to the nearest millionth of a dollar. Tariffs should be
// expressed per image, second, or million tokens/characters, not tiny unit prices.
func (u USD) Multiply(quantity float64, per int64) (USD, error) {
	if quantity < 0 || per <= 0 || u < 0 {
		return 0, errors.New("invalid billable quantity")
	}
	q, ok := new(big.Rat).SetString(strconv.FormatFloat(quantity, 'f', -1, 64))
	if !ok {
		return 0, errors.New("invalid billable quantity")
	}
	q.Mul(q, big.NewRat(int64(u), per))
	n, rem := new(big.Int), new(big.Int)
	n.QuoRem(q.Num(), q.Denom(), rem)
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("cost overflow")
	}
	return USD(n.Int64()), nil
}
