// Package limits defines admission quotas, independent of task execution.
package limits

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Values map[string]int

var Defaults = Values{
	"key_generations_minute": 100, "user_generations_minute": 300,
	"key_generations_second": 20, "user_generations_second": 50,
	"key_reads_minute": 1200, "user_reads_minute": 3600,
	"user_key_creations_minute": 5, "user_active_keys": 20,
}

type Config struct {
	Values  map[string]*int `json:"values"`
	Version int64           `json:"version"`
}
type Policy struct {
	System       Config `json:"system"`
	Organization Config `json:"organization"`
	Effective    Values `json:"effective"`
}

func (c Config) Validate(system bool) error {
	if len(c.Values) != len(Defaults) {
		return errors.New("all quota fields are required; use null to inherit an organization quota")
	}
	for name, v := range c.Values {
		if _, ok := Defaults[name]; !ok {
			return fmt.Errorf("unknown quota: %s", name)
		}
		if v == nil {
			if system {
				return fmt.Errorf("%s requires a system default", name)
			}
			continue
		}
		if *v < 1 || int64(*v) > 2147483647 {
			return fmt.Errorf("%s must be a positive 32-bit integer", name)
		}
	}
	return nil
}
func (p *Policy) Resolve() {
	p.Effective = Values{}
	for k, v := range Defaults {
		if n := p.System.Values[k]; n != nil {
			v = *n
		}
		if n := p.Organization.Values[k]; n != nil {
			v = *n
		}
		p.Effective[k] = v
	}
}

type Reader interface {
	RequestLimits(context.Context, string) (Policy, error)
}
type Repository interface {
	Reader
	LimitSettings(context.Context, string, string) (Policy, error)
	SaveLimits(context.Context, string, string, Config) (Policy, error)
}
type Bucket struct {
	ID     string
	Name   string
	Count  int
	Window time.Duration
}
type Limiter interface {
	Take(context.Context, []Bucket) error
}
type Exceeded struct {
	Name       string
	RetryAfter time.Duration
}

func (e *Exceeded) Error() string {
	message := "Too many requests"
	switch e.Name {
	case "key_generations_minute", "key_generations_second":
		message = "Too many generation requests for this API key"
	case "user_generations_minute", "user_generations_second":
		message = "Too many generation requests across your API keys"
	case "key_reads_minute":
		message = "Too many status requests for this API key"
	case "user_reads_minute":
		message = "Too many status requests across your API keys"
	case "user_key_creations_minute":
		message = "Too many API key creation requests"
	}
	seconds := max(int64(1), int64((e.RetryAfter+time.Second-1)/time.Second))
	return fmt.Sprintf("%s. Try again in %d seconds.", message, seconds)
}

var ErrActiveKeys = errors.New("active API key limit reached; revoke an unused key before creating another")

// Each workspace has an independent user counter shared by all of their keys.
// Personal keys use a separate workspace. No organization-wide shared counter.
func Buckets(v Values, org, user, key, operation string) []Bucket {
	workspace := org
	if workspace == "" {
		workspace = "personal"
	}
	userID := workspace + ":user:" + user
	var b []Bucket
	add := func(id, name string, window time.Duration) {
		b = append(b, Bucket{ID: id + ":" + name, Name: name, Count: v[name], Window: window})
	}
	switch operation {
	case "generation":
		for _, period := range []struct {
			name string
			d    time.Duration
		}{{"second", time.Second}, {"minute", time.Minute}} {
			if key != "" {
				add(workspace+":key:"+key, "key_generations_"+period.name, period.d)
			}
			add(userID, "user_generations_"+period.name, period.d)
		}
	case "read":
		if key != "" {
			add(workspace+":key:"+key, "key_reads_minute", time.Minute)
		}
		add(userID, "user_reads_minute", time.Minute)
	case "key_create":
		add(userID, "user_key_creations_minute", time.Minute)
	}
	return b
}
