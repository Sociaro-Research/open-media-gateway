package storage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type corsDocument struct {
	XMLName xml.Name      `xml:"CORSConfiguration"`
	Rules   []corsXMLRule `xml:"CORSRule"`
}
type corsXMLRule struct {
	ID      string   `xml:"ID,omitempty"`
	Origins []string `xml:"AllowedOrigin"`
	Methods []string `xml:"AllowedMethod"`
	Headers []string `xml:"AllowedHeader"`
	Expose  []string `xml:"ExposeHeader"`
	MaxAge  int32    `xml:"MaxAgeSeconds,omitempty"`
}

func TestBrowserSetupPreservesRulesAndHandlesPermissions(t *testing.T) {
	for _, endpoint := range []string{"https://s3.eu-central-003.backblazeb2.com", "https://account.r2.cloudflarestorage.com", "https://s3.eu-central-1.amazonaws.com", "https://storage.example.test"} {
		for _, scenario := range []string{"ready", "append", "absent", "read-denied", "write-denied", "read-missing-bucket", "network", "server", "redirect", "unconfirmed", "propagating"} {
			t.Run(endpoint+"/"+scenario, func(t *testing.T) {
				const origin = "https://gateway.example.test"
				old := corsXMLRule{ID: "other-app", Origins: []string{"https://other.example.test"}, Methods: []string{"GET"}, Headers: []string{"range"}, Expose: []string{"ETag"}, MaxAge: 120}
				document := corsDocument{Rules: []corsXMLRule{old}}
				reads, writes, options := 0, 0, 0
				remote := &memoryS3{objects: map[string][]byte{}, types: map[string]string{}}
				transport := referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
					w := httptest.NewRecorder()
					if r.Method == "OPTIONS" {
						options++
						if scenario == "network" {
							return nil, errors.New("network unavailable")
						}
						if scenario == "server" {
							w.WriteHeader(503)
							return w.Result(), nil
						}
						if scenario == "redirect" {
							w.Header().Set("Location", "https://foreign.test")
							w.WriteHeader(307)
							return w.Result(), nil
						}
						if scenario == "ready" || writes > 0 && scenario != "propagating" {
							w.Header().Set("Access-Control-Allow-Origin", origin)
							w.Header().Set("Access-Control-Allow-Methods", "PUT, GET, HEAD")
							w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
						} else {
							w.WriteHeader(403)
						}
						return w.Result(), nil
					}
					if !r.URL.Query().Has("cors") {
						return remote.RoundTrip(r)
					}
					if r.Header.Get("Authorization") == "" {
						t.Fatal("unsigned CORS request")
					}
					w.Header().Set("Content-Type", "application/xml")
					if r.Method == "GET" {
						reads++
						switch {
						case scenario == "read-denied":
							w.WriteHeader(403)
							fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
						case scenario == "read-missing-bucket":
							w.WriteHeader(404)
							fmt.Fprint(w, `<Error><Code>NoSuchBucket</Code></Error>`)
						case scenario == "absent" && writes == 0:
							w.WriteHeader(404)
							fmt.Fprint(w, `<Error><Code>NoSuchCORSConfiguration</Code></Error>`)
						case scenario == "unconfirmed" && writes > 0:
							_ = xml.NewEncoder(w).Encode(corsDocument{Rules: []corsXMLRule{old}})
						default:
							_ = xml.NewEncoder(w).Encode(document)
						}
					} else if r.Method == "PUT" {
						writes++
						data, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						checksum := md5.Sum(data)
						if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(checksum[:]) || !strings.Contains(r.Header.Get("Authorization"), "content-md5") {
							t.Fatal("CORS XML must carry a signed Content-MD5")
						}
						for name := range r.Header {
							if strings.HasPrefix(strings.ToLower(name), "x-amz-checksum") || strings.EqualFold(name, "x-amz-sdk-checksum-algorithm") {
								t.Fatal("unsupported flexible checksum", name)
							}
						}
						if scenario == "write-denied" {
							w.WriteHeader(403)
							fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
							return w.Result(), nil
						}
						document = corsDocument{}
						if err := xml.Unmarshal(data, &document); err != nil {
							t.Fatal(err)
						}
						if scenario != "absent" && !reflect.DeepEqual(document.Rules[0], old) {
							t.Fatal("foreign rule was changed", string(data))
						}
						added := document.Rules[len(document.Rules)-1]
						if !reflect.DeepEqual(added.Origins, []string{origin}) || !reflect.DeepEqual(added.Methods, []string{"PUT", "GET", "HEAD"}) || !reflect.DeepEqual(added.Headers, []string{"content-type", "authorization", "x-amz-*", "range"}) {
							t.Fatal("incorrect browser rule", string(data))
						}
					} else {
						t.Fatal("unexpected bucket mutation", r.Method)
					}
					return w.Result(), nil
				})
				m := newTestManager(t, newSettingsMemory(), remote)
				m.client = &http.Client{Transport: transport}
				input := s3Update()
				input.Endpoint = endpoint
				input.BrowserOrigin = origin
				// Test connection remains read-only even if the key could repair CORS.
				err := m.Check(context.Background(), input)
				if (err == nil) != (scenario == "ready") || reads != 0 || writes != 0 {
					t.Fatalf("read-only check: %v reads=%d writes=%d", err, reads, writes)
				}
				settings, err := m.Update(context.Background(), input)
				success := scenario == "ready" || scenario == "append" || scenario == "absent" || scenario == "propagating"
				if (err == nil) != success {
					t.Fatalf("save: %v", err)
				}
				if success {
					if settings.BrowserUploadsPending != (scenario != "ready") {
						t.Fatal("propagation status", settings)
					}
					before := writes
					if _, err = m.Update(context.Background(), input); err != nil {
						t.Fatal("idempotent save", err)
					}
					if writes != before {
						t.Fatal("repeated save changed policy", writes)
					}
				} else {
					if !errors.Is(err, ErrInvalidConfig) || len(m.repo.(*settingsMemory).active) != 0 {
						t.Fatalf("failed setup activated storage: %v", err)
					}
					for _, secret := range []string{input.SecretAccessKey, "X-Amz-Signature"} {
						if strings.Contains(err.Error(), secret) {
							t.Fatal("error exposed credentials")
						}
					}
				}
				if (scenario == "network" || scenario == "server" || scenario == "redirect" || scenario == "ready") && (reads != 0 || writes != 0) {
					t.Fatal("unnecessary CORS administration", reads, writes)
				}
				if (scenario == "read-denied" || scenario == "read-missing-bucket") && writes != 0 {
					t.Fatal("read failure replaced policy")
				}
				if options == 0 {
					t.Fatal("browser check was skipped")
				}
			})
		}
	}
}

func TestBrowserRuleMergeAndOriginValidation(t *testing.T) {
	const origin = "https://gateway.example.test"
	rules, changed, err := mergeBrowserRule(nil, origin)
	if err != nil || !changed {
		t.Fatal(err)
	}
	rules[0].AllowedMethods = append(rules[0].AllowedMethods, "DELETE")
	rules[0].AllowedHeaders = append(rules[0].AllowedHeaders, "custom-header")
	rules[0].MaxAgeSeconds = aws.Int32(120)
	again, changed, err := mergeBrowserRule(rules, origin)
	if err != nil || changed || !reflect.DeepEqual(again, rules) {
		t.Fatal("idempotence or preservation", err)
	}
	rules[0].AllowedMethods = []string{"GET"}
	again, changed, err = mergeBrowserRule(rules, origin)
	if err != nil || !changed || len(again) != 1 || len(again[0].AllowedMethods) != 3 {
		t.Fatal("managed rule upgrade", err)
	}
	rules[0].AllowedOrigins = []string{"https://foreign.test"}
	if _, _, err = mergeBrowserRule(rules, origin); err == nil {
		t.Fatal("ID collision widened foreign rule")
	}
	if _, _, err = mergeBrowserRule(make([]types.CORSRule, 100), origin); err == nil {
		t.Fatal("rule limit ignored")
	}
	for _, origin := range []string{"*", "null", "https://*.example.test", "https://example.test/path", "https://user@example.test", "https://example.test?q=1", "http://remote.test"} {
		remote := &memoryS3{objects: map[string][]byte{}, types: map[string]string{}}
		m := newTestManager(t, newSettingsMemory(), remote)
		input := s3Update()
		input.BrowserOrigin = origin
		if _, err := m.Update(context.Background(), input); !errors.Is(err, ErrInvalidConfig) || len(remote.objects) != 0 {
			t.Fatal("invalid origin accepted", origin, err)
		}
	}
}

func TestBrowserSetupFailureRetainsSavedConnection(t *testing.T) {
	remote := &memoryS3{objects: map[string][]byte{}, types: map[string]string{}}
	m := newTestManager(t, newSettingsMemory(), remote)
	old := s3Update()
	if _, err := m.Update(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	before, err := m.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.client = &http.Client{Transport: referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "OPTIONS" && !r.URL.Query().Has("cors") {
			return remote.RoundTrip(r)
		}
		w := httptest.NewRecorder()
		w.WriteHeader(403)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
		return w.Result(), nil
	})}
	input := old
	input.Bucket = "new-destination"
	input.BrowserOrigin = "https://gateway.example.test"
	if _, err := m.Update(context.Background(), input); err == nil {
		t.Fatal("denied CORS setup succeeded")
	}
	after, err := m.Get(context.Background())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed setup replaced active connection", before, after, err)
	}
}
