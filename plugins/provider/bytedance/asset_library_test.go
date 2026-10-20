package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestAssetLibrarySignedProtocolAndNoRetry(t *testing.T) {
	calls := 0
	fail := false
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "ark.ap-southeast-1.byteplusapi.com" || !strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=asset-ak/") || r.Header.Get("X-Date") == "" || r.Header.Get("X-Content-Sha256") == "" {
			t.Fatal("wrong asset origin/signature", r.URL, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["ProjectName"] != "characters" || body["Moderation"] != nil {
			t.Fatal("project or moderation changed", body)
		}
		result := `{"Id":"asset-test"}`
		switch r.URL.Query().Get("Action") {
		case "CreateAsset":
			if fail {
				return nil, errors.New("connection reset")
			}
			if body["URL"] != "https://storage.example/reference.png" || body["AssetType"] != "Image" || body["GroupId"] != "group-test" {
				t.Fatal(body)
			}
		case "GetAsset":
			result = `{"Id":"asset-test","GroupId":"group-test","Name":"Character","AssetType":"Image","Status":"Active","URL":"https://preview.example/image.png"}`
		case "ListAssets":
			if _, exists := body["NextToken"]; exists {
				t.Fatal("first page must omit empty NextToken")
			}
			result = `{"Items":[{"Id":"asset-test","GroupId":"group-test","AssetType":"Image","Status":"Active"}],"NextToken":"next"}`
		case "CreateAssetGroup":
			result = `{"Id":"group-test"}`
			if body["GroupType"] != "AIGC" {
				t.Fatal(body)
			}
		case "ListAssetGroups":
			if _, exists := body["NextToken"]; exists {
				t.Fatal("first group page must omit empty NextToken")
			}
			result = `{"Items":[{"Id":"group-test","Name":"Characters"}]}`
		case "DeleteAsset":
			result = `{}`
		default:
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Result":` + result + `}`))}, nil
	})}
	c := providerkit.Credentials{APIKey: "inference-key", BaseURL: "https://ark.ap-southeast.bytepluses.com/api/v3", BillingAccessKeyID: "billing-ak", BillingAccessKeySecret: "billing-sk"}
	c.Set("asset_access_key_id", "asset-ak")
	c.Set("asset_access_key_secret", "asset-sk")
	c.Set("asset_project", "characters")
	library, err := newAssetLibrary(c, client)
	if err != nil {
		t.Fatal(err)
	}
	g, err := library.CreateGroup(context.Background(), "Characters")
	if err != nil || g.ID != "group-test" {
		t.Fatal(g, err)
	}
	if groups, err := library.Groups(context.Background()); err != nil || len(groups) != 1 {
		t.Fatal(groups, err)
	}
	a, err := library.Create(context.Background(), g.ID, "https://storage.example/reference.png", "Character", "Image")
	if err != nil || a.Status != "Processing" {
		t.Fatal(a, err)
	}
	a, err = library.Get(context.Background(), a.ID)
	if err != nil || a.URI != "asset://asset-test" || a.Status != "Active" {
		t.Fatal(a, err)
	}
	page, err := library.List(context.Background(), g.ID, "", "")
	if err != nil || page.Next != "next" || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if err = library.Delete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	before := calls
	fail = true
	if _, err = library.Create(context.Background(), g.ID, "https://storage.example/reference.png", "Character", "Image"); err == nil || calls-before != 1 {
		t.Fatal("uncertain create repeated", err, calls-before)
	}
	if _, err = library.Get(context.Background(), "../secret"); !errors.Is(err, providerassets.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestAssetLibraryCredentialsDoNotChangeInferenceIdentity(t *testing.T) {
	c := providerkit.Credentials{APIKey: "inference", BaseURL: "https://ark.ap-southeast.bytepluses.com/api/v3"}
	before := Definition().Identity(c)
	c.Set("asset_access_key_id", "asset-ak")
	c.Set("asset_access_key_secret", "asset-sk")
	c.Set("asset_project", "characters")
	if Definition().Identity(c) != before {
		t.Fatal("management credentials changed inference identity")
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{{"dreamina-seedance-2-0-260128", true}, {"dreamina-seedance-2-5-260628", true}, {"seedance-1-5-pro-251215", false}, {"seedream-4-5-251128", false}} {
		if got := describeModel(tc.id).ReferencePromptSyntax == "numbered"; got != tc.want {
			t.Fatalf("%s: %v", tc.id, got)
		}
	}
}
