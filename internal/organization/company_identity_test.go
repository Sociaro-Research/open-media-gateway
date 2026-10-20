package organization

import "testing"

func TestCompanyDomainNormalization(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"https://WWW.Example.COM./about?campaign=1#team", "example.com"}, {"example.com", "example.com"}, {"http://example.com:8080/path", "example.com"}, {"//www.example.com/", "example.com"}, {" https://παράδειγμα.δοκιμή/page ", "xn--hxajbheg2az3al.xn--jxalpdlp"}, {"https://xn--hxajbheg2az3al.xn--jxalpdlp", "xn--hxajbheg2az3al.xn--jxalpdlp"}, {"https://studio.example.com", "studio.example.com"},
	} {
		got, e := CompanyDomain(test.input)
		if e != nil || got != test.want {
			t.Fatalf("%q: %s %v", test.input, got, e)
		}
	}
	for _, input := range []string{"", " ", "localhost", "ftp://example.com", "https://user:password@example.com", "https://example..com", "https://-example.com", "https://example_.com", "https://127.0.0.1", "https://example.com:bad", "https://example.com\\evil", "https://exam\nple.com"} {
		if domain, e := CompanyDomain(input); e == nil {
			t.Fatalf("accepted invalid %q: %q", input, domain)
		}
	}
	if name := CompanyName(" \tAcme\u00a0  Team\n"); name != "Acme Team" {
		t.Fatal(name)
	}
}
