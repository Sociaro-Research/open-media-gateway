package kling

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func resetAccountGate(t *testing.T) {
	t.Helper()
	accountQueryGate <- struct{}{}
	nextAccountQuery = time.Time{}
	<-accountQueryGate
}

func TestAccountAuthenticationIsFreeScopedCachedAndConcurrent(t *testing.T) {
	resetAccountGate(t)
	var calls atomic.Int32
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Host != "api-singapore.klingai.com" || r.URL.Path != "/account/costs" || r.Header.Get("Authorization") != "Bearer fixture-secret" || !providerkit.IsCredentialCheck(r.Context()) {
			t.Error("incorrect check contract")
		}
		start, e1 := strconv.ParseInt(r.URL.Query().Get("start_time"), 10, 64)
		end, e2 := strconv.ParseInt(r.URL.Query().Get("end_time"), 10, 64)
		if e1 != nil || e2 != nil || end-start != int64(24*time.Hour/time.Millisecond) || time.Since(time.UnixMilli(end)) > time.Second || len(r.URL.Query()) != 2 {
			t.Error("incorrect millisecond query window")
		}
		return response(200, `{"code":0,"data":{"code":0,"resource_pack_subscribe_infos":[{"resource_pack_name":"private-account-data","remaining_quantity":42}]}}`), nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := a.CheckCredentials(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	status := a.CredentialStatus()
	if calls.Load() != 1 || status.CheckedAt.IsZero() || status.Err != nil {
		t.Fatal(calls.Load(), status)
	}
	a.checkMu.Lock()
	a.check.CheckedAt = time.Now().Add(-credentialCacheTTL)
	a.checkMu.Unlock()
	if !a.CredentialStatus().CheckedAt.IsZero() {
		t.Fatal("expired verification retained")
	}
}

func TestAccountQueryFailuresCannotAuthenticateOrLeakProviderText(t *testing.T) {
	for name, fixture := range map[string]struct {
		status   int
		body     string
		contains string
	}{
		"bad key":                          {401, `{"code":1001,"message":"fixture-secret private account"}`, "authentication failed"},
		"restricted account":               {403, `{"code":1103,"message":"fixture-secret private account"}`, "permission"},
		"unknown forbidden":                {403, `{"message":"fixture-secret private account"}`, "permission"},
		"nested failure":                   {200, `{"code":0,"data":{"code":1103,"resource_pack_subscribe_infos":[]}}`, "permission"},
		"missing nested code":              {200, `{"code":0,"data":{"resource_pack_subscribe_infos":[]}}`, "malformed"},
		"non-success HTTP with zero codes": {403, `{"code":0,"data":{"code":0}}`, "permission"},
		"missing nested code and packages": {200, `{"code":0,"data":{}}`, "malformed"},
		"null nested code":                 {200, `{"code":0,"data":{"code":null}}`, "malformed"},
		"fractional nested code":           {200, `{"code":0,"data":{"code":0.5}}`, "malformed"},
		"wrong nested code type":           {200, `{"code":0,"data":{"code":"0"}}`, "malformed"},
		"non-object data":                  {200, `{"code":0,"data":[]}`, "malformed"},
		"null data":                        {200, `{"code":0,"data":null}`, "no data"},
		"malformed envelope":               {200, `{"data":{"code":0,"resource_pack_subscribe_infos":[]}}`, "malformed"},
	} {
		t.Run(name, func(t *testing.T) {
			resetAccountGate(t)
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(fixture.status, fixture.body), nil
			})
			err := a.CheckCredentials(context.Background())
			if err == nil || !strings.Contains(err.Error(), fixture.contains) || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private account") || a.CredentialStatus().Err == nil {
				t.Fatal(err)
			}
			_ = a.CheckCredentials(context.Background())
			if calls != 1 {
				t.Fatal("immediate failed check was not rate limited")
			}
		})
	}
	resetAccountGate(t)
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":0,"data":{"code":0,"resource_pack_subscribe_infos":[]}}`), nil
	})
	if err := a.CheckCredentials(context.Background()); err != nil {
		t.Fatal("empty account packages do not imply invalid credentials", err)
	}
}

func TestAccountQueryGateAppliesAcrossConnectionsAndRespectsCancellation(t *testing.T) {
	resetAccountGate(t)
	var starts []time.Time
	f := transport(func(*http.Request) (*http.Response, error) {
		starts = append(starts, time.Now())
		return response(200, `{"code":0,"data":{"code":0,"resource_pack_subscribe_infos":[]}}`), nil
	})
	if err := adapter(t, f).CheckCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := adapter(t, f).CheckCredentials(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := adapter(t, f).CheckCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(starts) != 2 || starts[1].Sub(starts[0]) < 990*time.Millisecond {
		t.Fatal("fresh account queries exceed QPS=1", starts)
	}
}

// These synthetic bodies cover the deployed error branch. The retained live
// evidence has no raw response and cannot distinguish missing/null/non-array.
func TestAccountAuthenticationDoesNotRequireResourcePackageDetails(t *testing.T) {
	for name, packages := range map[string]string{
		"absent": "", "null": `,"resource_pack_subscribe_infos":null`,
		"wrong shape":     `,"resource_pack_subscribe_infos":{"private-account-data":"fixture-secret"}`,
		"empty list":      `,"resource_pack_subscribe_infos":[]`,
		"documented list": `,"resource_pack_subscribe_infos":[{"resource_pack_name":"private-account-data","remaining_quantity":42}]`,
	} {
		t.Run(name, func(t *testing.T) {
			resetAccountGate(t)
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, `{"code":0,"message":"fixture-secret private-account-data","data":{"code":0`+packages+`}}`), nil
			})
			if err := a.CheckCredentials(context.Background()); err != nil {
				t.Fatal("successful service status rejected", err)
			}
			status := a.CredentialStatus()
			wantWarning := name != "empty list" && name != "documented list"
			if status.Err != nil || status.CheckedAt.IsZero() || (status.Warning != "") != wantWarning {
				t.Fatal(status)
			}
			if wantWarning && status.Warning != packageDetailsWarning {
				t.Fatal("unsafe warning", status.Warning)
			}
			if strings.Contains(status.Warning, "fixture-secret") || strings.Contains(status.Warning, "private-account-data") {
				t.Fatal("account contents leaked")
			}
			if err := a.CheckCredentials(context.Background()); err != nil || calls != 1 || a.CredentialStatus().Warning != status.Warning {
				t.Fatal("cached scope/warning lost", calls, err)
			}
		})
	}
}

func TestFreshAccountCheckReplacesOptionalDetailsWarning(t *testing.T) {
	resetAccountGate(t)
	calls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return response(200, `{"code":0,"data":{"code":0}}`), nil
		case 2:
			return response(200, `{"code":0,"data":{"code":0,"resource_pack_subscribe_infos":[]}}`), nil
		default:
			return response(401, `{"code":1002,"message":"fixture-secret private-account-data"}`), nil
		}
	})
	if err := a.CheckCredentials(context.Background()); err != nil || a.CredentialStatus().Warning == "" {
		t.Fatal(err)
	}
	for attempt := 2; attempt <= 3; attempt++ {
		a.checkMu.Lock()
		a.check.CheckedAt = time.Now().Add(-credentialCacheTTL)
		a.checkMu.Unlock()
		resetAccountGate(t)
		err := a.CheckCredentials(context.Background())
		if calls != attempt || a.CredentialStatus().Warning != "" || (err != nil) != (attempt == 3) {
			t.Fatal("stale warning or authentication retained", calls, a.CredentialStatus(), err)
		}
	}
}
