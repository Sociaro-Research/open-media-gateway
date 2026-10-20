package money

import (
	"encoding/json"
	"errors"
)

// Percent stores hundredths of a percent. 2500 represents a 25% markup.
type Percent int64

func (p Percent) Validate() error {
	if p < 0 || p > 100000 {
		return errors.New("markup must be between 0 and 1000 percent, with at most two decimal places")
	}
	return nil
}
func (p Percent) MarshalJSON() ([]byte, error) { return USD(int64(p) * 10000).MarshalJSON() }
func (p *Percent) UnmarshalJSON(b []byte) error {
	var value USD
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	if value%10000 != 0 {
		return errors.New("markup supports at most two decimal places")
	}
	next := Percent(value / 10000)
	if err := next.Validate(); err != nil {
		return err
	}
	*p = next
	return nil
}
func (p Percent) Apply(base USD) (USD, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	return base.Multiply(float64(10000+p), 10000)
}
