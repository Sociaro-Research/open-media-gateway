package limits

import "testing"

func TestInheritanceAndWorkspaceCounters(t *testing.T) {
	p := Policy{System: Config{Values: map[string]*int{}}, Organization: Config{Values: map[string]*int{}}}
	for k, v := range Defaults {
		p.System.Values[k] = new(int)
		*p.System.Values[k] = v
		p.Organization.Values[k] = nil
	}
	if err := p.System.Validate(true); err != nil {
		t.Fatal(err)
	}
	if err := p.Organization.Validate(false); err != nil {
		t.Fatal(err)
	}
	n := 7
	p.Organization.Values["user_generations_minute"] = &n
	p.Resolve()
	if p.Effective["user_generations_minute"] != 7 || p.Effective["key_generations_minute"] != 100 {
		t.Fatal(p)
	}
	a := Buckets(p.Effective, "org", "user", "key1", "generation")
	b := Buckets(p.Effective, "org", "user", "key2", "generation")
	c := Buckets(p.Effective, "other", "user", "key1", "generation")
	if a[1].ID != b[1].ID || a[0].ID == b[0].ID || a[1].ID == c[1].ID {
		t.Fatal("incorrect identity scope")
	}
	n = 0
	if p.Organization.Validate(false) == nil {
		t.Fatal("zero bypass allowed")
	}
	delete(p.System.Values, "user_active_keys")
	if p.System.Validate(true) == nil {
		t.Fatal("missing default allowed")
	}
}
