package money

import (
	"encoding/json"
	"testing"
)

func TestExactUSD(t *testing.T) {
	a, _ := Parse("0.50")
	b, _ := Parse("0.05")
	if (a + b).String() != "0.550000" {
		t.Fatal(a + b)
	}
	for _, v := range []string{"NaN", "Infinity", "0.0000001", "999999999999999999999"} {
		if _, err := Parse(v); err == nil {
			t.Fatal(v)
		}
	}
	var amount USD
	if err := json.Unmarshal([]byte("0.000001"), &amount); err != nil || amount != 1 {
		t.Fatal(amount, err)
	}
	rate, _ := Parse("5.6")
	cost, err := rate.Multiply(70726, 1_000_000)
	if err != nil || cost.String() != "0.396066" {
		t.Fatal(cost, err)
	}
}
