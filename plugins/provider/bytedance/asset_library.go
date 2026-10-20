package bytedance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	bytebase "github.com/byteplus-sdk/byteplus-go-sdk-v2/byteplus/base"
)

type assetLibrary struct {
	credentials bytebase.Credentials
	project     string
	client      *http.Client
}

func newAssetLibrary(c providerkit.Credentials, client *http.Client) (providerassets.Client, error) {
	ak, sk := c.Get("asset_access_key_id"), c.Get("asset_access_key_secret")
	if ak == "" || sk == "" {
		return nil, providerassets.ErrUnavailable
	}
	region := c.Get("asset_region")
	if region == "" {
		region = "ap-southeast-1"
	}
	if region != "ap-southeast-1" || !strings.Contains(c.BaseURL, "ark.ap-southeast.bytepluses.com") {
		return nil, fmt.Errorf("%w: the asset library currently requires the Johor inference region", providerassets.ErrUnavailable)
	}
	project := c.Get("asset_project")
	if project == "" {
		project = "default"
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return fmt.Errorf("asset API redirects are not supported") }
	return &assetLibrary{credentials: bytebase.Credentials{AccessKeyID: ak, SecretAccessKey: sk, Region: region, Service: "ark"}, project: project, client: &copied}, nil
}
func (a *assetLibrary) Scope() string {
	sum := sha256.Sum256([]byte(a.credentials.AccessKeyID + "\x00" + a.credentials.Region + "\x00" + a.project))
	return hex.EncodeToString(sum[:])
}
func (a *assetLibrary) call(ctx context.Context, action string, input map[string]any, output any) error {
	input["ProjectName"] = a.project
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	endpoint := "https://ark." + a.credentials.Region + ".byteplusapi.com/?Action=" + url.QueryEscape(action) + "&Version=2024-01-01"
	r, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	a.credentials.Sign(r)
	response, err := a.client.Do(r)
	if err != nil {
		return fmt.Errorf("BytePlus asset request failed; do not repeat a create until its outcome is checked")
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return fmt.Errorf("invalid BytePlus asset response")
	}
	var envelope struct {
		Result           json.RawMessage
		ResponseMetadata struct{ Error *struct{ Code string } }
	}
	if json.Unmarshal(b, &envelope) != nil {
		return fmt.Errorf("invalid BytePlus asset JSON response")
	}
	if response.StatusCode == 404 {
		if action == "DeleteAsset" {
			return nil
		}
		return providerassets.ErrNotFound
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("BytePlus asset request outcome is uncertain")
	}
	code := ""
	if e := envelope.ResponseMetadata.Error; e != nil && regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`).MatchString(e.Code) {
		code = e.Code
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.ResponseMetadata.Error != nil {
		return fmt.Errorf("%w: BytePlus rejected %s (%s; HTTP %d); check asset permissions, project and account rights", providerassets.ErrRejected, action, code, response.StatusCode)
	}
	if output != nil && (len(envelope.Result) == 0 || json.Unmarshal(envelope.Result, output) != nil) {
		return fmt.Errorf("invalid BytePlus asset result")
	}
	return nil
}

type assetWire struct {
	ID        string `json:"Id"`
	GroupID   string `json:"GroupId"`
	Name      string
	AssetType string
	Status    string
	URL       string
}

func (w assetWire) view() providerassets.Asset {
	failure := ""
	if w.Status == "Failed" {
		failure = "BytePlus could not prepare this asset. Check its format, dimensions and project."
	}
	return providerassets.Asset{Failure: failure, ID: w.ID, GroupID: w.GroupID, Name: w.Name, Type: w.AssetType, Status: w.Status, PreviewURL: w.URL, URI: "asset://" + w.ID}
}
func (a *assetLibrary) Groups(ctx context.Context) ([]providerassets.Group, error) {
	result := []providerassets.Group{}
	next := ""
	for page := 0; page < 100; page++ {
		var out struct {
			Items []struct {
				ID   string `json:"Id"`
				Name string
			}
			NextToken string
		}
		input := map[string]any{"Filter": map[string]any{"GroupType": "AIGC"}, "MaxResults": 100}
		if next != "" {
			input["NextToken"] = next
		}
		if err := a.call(ctx, "ListAssetGroups", input, &out); err != nil {
			return nil, err
		}
		for _, g := range out.Items {
			result = append(result, providerassets.Group{ID: g.ID, Name: g.Name})
		}
		if out.NextToken == "" {
			return result, nil
		}
		if out.NextToken == next {
			return nil, fmt.Errorf("repeated asset group page")
		}
		next = out.NextToken
	}
	return nil, fmt.Errorf("too many asset groups")
}
func (a *assetLibrary) CreateGroup(ctx context.Context, name string) (providerassets.Group, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 64 {
		return providerassets.Group{}, providerassets.ErrInvalid
	}
	err := a.call(ctx, "CreateAssetGroup", map[string]any{"Name": name, "GroupType": "AIGC"}, &out)
	if err == nil && !strings.HasPrefix(out.ID, "group-") {
		err = fmt.Errorf("invalid asset group ID")
	}
	return providerassets.Group{ID: out.ID, Name: name}, err
}
func (a *assetLibrary) List(ctx context.Context, group, next, name string) (providerassets.Page, error) {
	filter := map[string]any{"GroupType": "AIGC"}
	if group != "" {
		filter["GroupIds"] = []string{group}
	}
	if name != "" {
		filter["Name"] = name
	}
	var out struct {
		Items     []assetWire
		NextToken string
	}
	input := map[string]any{"Filter": filter, "MaxResults": 48, "SortBy": "CreateTime", "SortOrder": "Desc"}
	if next != "" {
		input["NextToken"] = next
	}
	err := a.call(ctx, "ListAssets", input, &out)
	page := providerassets.Page{Items: []providerassets.Asset{}, Next: out.NextToken}
	for _, item := range out.Items {
		if core.IsAssetReference("asset://" + item.ID) {
			page.Items = append(page.Items, item.view())
		}
	}
	return page, err
}
func (a *assetLibrary) Get(ctx context.Context, id string) (providerassets.Asset, error) {
	if !core.IsAssetReference("asset://" + id) {
		return providerassets.Asset{}, providerassets.ErrInvalid
	}
	var out assetWire
	err := a.call(ctx, "GetAsset", map[string]any{"Id": id}, &out)
	if err == nil && (out.ID != id || out.GroupID == "" || (out.Status != "Active" && out.Status != "Processing" && out.Status != "Failed") || (out.AssetType != "Image" && out.AssetType != "Video" && out.AssetType != "Audio")) {
		err = fmt.Errorf("invalid asset response")
	}
	return out.view(), err
}
func (a *assetLibrary) Create(ctx context.Context, group, source, name, kind string) (providerassets.Asset, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := a.call(ctx, "CreateAsset", map[string]any{"GroupId": group, "URL": source, "Name": name, "AssetType": kind}, &out)
	if err == nil && !core.IsAssetReference("asset://"+out.ID) {
		err = fmt.Errorf("invalid created asset ID")
	}
	return providerassets.Asset{ID: out.ID, GroupID: group, Name: name, Type: kind, Status: "Processing", URI: "asset://" + out.ID}, err
}
func (a *assetLibrary) Delete(ctx context.Context, id string) error {
	if !core.IsAssetReference("asset://" + id) {
		return providerassets.ErrInvalid
	}
	return a.call(ctx, "DeleteAsset", map[string]any{"Id": id}, nil)
}
