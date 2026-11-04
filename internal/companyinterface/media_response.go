package companyinterface

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

type mediaFields map[string]json.RawMessage

func fieldString(f mediaFields, key string) (string, error) {
	var value string
	if json.Unmarshal(f[key], &value) != nil {
		return "", ErrUnavailable
	}
	return value, nil
}
func mediaPath(raw, central, company string) (string, string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Path == "" || u.EscapedPath() != u.Path {
		return "", "", ErrUnavailable
	}
	if u.IsAbs() {
		if u.Scheme+"://"+u.Host != central {
			return "", "", ErrUnavailable
		}
	} else if u.Host != "" || !strings.HasPrefix(raw, "/") {
		return "", "", ErrUnavailable
	}
	c := Classify("GET", u.Path)
	if !c.Stream {
		return "", "", ErrUnavailable
	}
	if _, e = RequestContract("GET", u.Path, u.RawQuery, nil, Principal{Grant: Grant{Company: company}, Kind: CentralKey, Scopes: []string{c.Scope}}); e != nil {
		return "", "", ErrUnavailable
	}
	return u.Path, u.RawQuery, nil
}
func mapMediaURL(raw, central, company string, allowed func(string, url.Values) bool) (string, error) {
	path, query, e := mediaPath(raw, central, company)
	if e != nil {
		return "", e
	}
	values, e := url.ParseQuery(query)
	if e != nil || !allowed(path, values) {
		return "", ErrUnavailable
	}
	mapped := LocalCustomerPrefix + strings.TrimPrefix(path, "/")
	if query != "" {
		mapped += "?" + query
	}
	return mapped, nil
}
func mapMediaField(f mediaFields, key, central, company string, allowed func(string, url.Values) bool) error {
	value, ok := f[key]
	if !ok || string(value) == "null" {
		return nil
	}
	var raw string
	if json.Unmarshal(value, &raw) != nil {
		return ErrUnavailable
	}
	if raw == "" {
		return nil
	}
	mapped, e := mapMediaURL(raw, central, company, allowed)
	if e != nil {
		return e
	}
	f[key], _ = json.Marshal(mapped)
	return nil
}
func mapMediaArray(f mediaFields, key string, apply func(mediaFields) error) error {
	raw, ok := f[key]
	if !ok || string(raw) == "null" {
		return nil
	}
	var rows []mediaFields
	if json.Unmarshal(raw, &rows) != nil {
		return ErrUnavailable
	}
	for _, row := range rows {
		if row == nil {
			return ErrUnavailable
		}
		if e := apply(row); e != nil {
			return e
		}
	}
	f[key], _ = json.Marshal(rows)
	return nil
}
func mapGenerationMedia(f mediaFields, company, central string) error {
	id, e := fieldString(f, "id")
	if e != nil || !validID(id) {
		return ErrUnavailable
	}
	object, e := fieldString(f, "object")
	if e != nil || (object != "video" && object != "generation") {
		return ErrUnavailable
	}
	base := "/v1/generations/" + id
	if object == "video" {
		base = "/v1/videos/" + id
	}
	if raw, ok := f["organization_id"]; ok {
		var companyID string
		if json.Unmarshal(raw, &companyID) != nil || companyID != "" && companyID != company {
			return ErrUnavailable
		}
	}
	// Status is independently validated, including list/report projections that
	// do not pass through the single-generation response adapter.
	status, e := fieldString(f, "status_url")
	if e != nil {
		return e
	}
	mappedStatus := LocalCustomerPrefix + strings.TrimPrefix(base, "/")
	if status != base && status != mappedStatus {
		return ErrUnavailable
	}
	f["status_url"], _ = json.Marshal(mappedStatus)
	if e = mapMediaField(f, "content_url", central, company, func(p string, q url.Values) bool { return p == base+"/content" && len(q) == 0 }); e != nil {
		return e
	}
	if e = mapMediaField(f, "last_frame_url", central, company, func(p string, q url.Values) bool { return p == "/v1/generations/"+id+"/last-frame" && len(q) == 0 }); e != nil {
		return e
	}
	for _, key := range []string{"image", "input_reference"} {
		if value, ok := f[key]; ok {
			mapped, err := transformMediaValue(value, func(value string) (string, error) { return mapCentralInputURL(value, company, central) }, true)
			if err != nil {
				return err
			}
			f[key] = mapped
		}
	}
	if err := mapMediaOptions(f, company, central); err != nil {
		return err
	}
	return mapMediaArray(f, "artifacts", func(a mediaFields) error {
		artifact, e := fieldString(a, "id")
		if e != nil || !core.ValidGeneratedArtifactID(artifact) {
			return ErrUnavailable
		}
		return mapMediaField(a, "content_url", central, company, func(p string, q url.Values) bool {
			return p == "/v1/generations/"+id+"/artifacts/"+artifact+"/content" && len(q) == 0
		})
	})
}
func mediaJSONResponse(c Contract, status int, method, path, company, central string, raw []byte) ([]byte, error) {
	if status < 200 || status >= 300 || method == http.MethodHead {
		return raw, nil
	}
	switch c.ResponseURLs {
	case "generation", "template-generations", "resource-generation", "generations", "media-access", "reference-list", "template-gallery", "template-detail", "template-editing", "report-detail", "catalog":
	default:
		return raw, nil
	}
	var f mediaFields
	if json.Unmarshal(raw, &f) != nil || f == nil {
		return nil, ErrUnavailable
	}
	var e error
	catalog := func(row mediaFields) error {
		return mapMediaField(row, "icon_url", central, company, func(p string, q url.Values) bool {
			parts := strings.Split(p, "/")
			id, err := fieldString(row, "id")
			return err == nil && len(parts) == 5 && parts[1] == "provider-assets" && parts[3] == id
		})
	}
	switch c.ResponseURLs {
	case "generation", "template-generations", "resource-generation":
		e = mapGenerationMedia(f, company, central)
	case "generations":
		e = mapMediaArray(f, "data", func(row mediaFields) error { return mapGenerationMedia(row, company, central) })
	case "media-access":
		parts := strings.Split(path, "/")
		id := parts[len(parts)-2]
		link := func(key, artifact string) error {
			return mapMediaField(f, key, central, company, func(p string, q url.Values) bool {
				return p == "/media/"+id && q.Get("artifact") == artifact && ((key == "download_url" && q.Get("download") == "1") || (key != "download_url" && q.Get("download") == ""))
			})
		}
		for _, key := range []string{"preview_url", "download_url", "last_frame_url"} {
			artifact := ""
			if key == "last_frame_url" {
				artifact = "last-frame"
			}
			if e = link(key, artifact); e != nil {
				break
			}
		}
		if e == nil {
			e = mapMediaArray(f, "artifacts", func(a mediaFields) error {
				idValue, err := fieldString(a, "id")
				if err != nil || !core.ValidGeneratedArtifactID(idValue) {
					return ErrUnavailable
				}
				for _, key := range []string{"preview_url", "download_url"} {
					if err = mapMediaField(a, key, central, company, func(p string, q url.Values) bool {
						return p == "/media/"+id && q.Get("artifact") == idValue && ((key == "download_url" && q.Get("download") == "1") || (key == "preview_url" && q.Get("download") == ""))
					}); err != nil {
						return err
					}
				}
				return nil
			})
		}
	case "reference-list":
		owner := company
		if c.ID == "shared-template-library" {
			owner = templates.LibraryOwnerID
		}
		e = mapMediaArray(f, "data", func(a mediaFields) error {
			id, err := fieldString(a, "id")
			if err != nil || !validID(id) {
				return ErrUnavailable
			}
			return mapMediaField(a, "url", central, company, func(p string, q url.Values) bool { return p == "/references/"+owner+"/"+id })
		})
	case "template-editing":
		e = mapMediaOptions(f, company, central)
	case "template-gallery":
		e = mapMediaArray(f, "data", func(a mediaFields) error {
			return mapMediaField(a, "cover_url", central, company, func(p string, q url.Values) bool { return strings.HasPrefix(p, "/template-media/") })
		})
	case "template-detail":
		e = mapMediaArray(f, "fixed_references", func(a mediaFields) error {
			id, err := fieldString(a, "asset_id")
			if err != nil || !validID(id) {
				return ErrUnavailable
			}
			return mapMediaField(a, "url", central, company, func(p string, q url.Values) bool { return p == "/template-media/"+id })
		})
		if e == nil {
			e = mapMediaArray(f, "catalog", catalog)
		}
	case "catalog":
		if path == "/v1/providers" {
			e = mapMediaArray(f, "data", catalog)
		}
	case "report-detail":
		parts := strings.Split(path, "/")
		id := parts[len(parts)-1]
		if raw, ok := f["generation"]; ok {
			var generation mediaFields
			if json.Unmarshal(raw, &generation) != nil {
				return nil, ErrUnavailable
			}
			actual, err := fieldString(generation, "id")
			if err != nil || actual != id {
				return nil, ErrUnavailable
			}
			if e = mapGenerationMedia(generation, company, central); e == nil {
				f["generation"], _ = json.Marshal(generation)
			}
		}
		if raw, ok := f["links"]; ok && e == nil {
			var links mediaFields
			if json.Unmarshal(raw, &links) != nil {
				return nil, ErrUnavailable
			}
			for _, key := range []string{"content_url", "download_url", "last_frame_url"} {
				target := path + "/content"
				if key == "last_frame_url" {
					target = path + "/last-frame"
				}
				if e = mapMediaField(links, key, central, company, func(p string, q url.Values) bool {
					return p == target && ((key == "download_url" && q.Get("download") == "1") || (key != "download_url" && len(q) == 0))
				}); e != nil {
					break
				}
			}
			if e == nil {
				f["links"], _ = json.Marshal(links)
			}
		}
	}
	if e != nil {
		return nil, e
	}
	return json.Marshal(f)
}

// These option leaves are defined by MediaOptions. Unrelated provider parameters
// and text fields are retained byte-for-byte as raw JSON values.
func mapMediaOptions(f mediaFields, company, central string) error {
	raw, ok := f["options"]
	if !ok || string(raw) == "null" {
		return nil
	}
	var options mediaFields
	if json.Unmarshal(raw, &options) != nil || options == nil {
		return ErrUnavailable
	}
	for _, key := range []string{"image_urls", "video_url", "video_urls", "audio_url", "audio_urls"} {
		if value, ok := options[key]; ok {
			mapped, e := transformMediaValue(value, func(value string) (string, error) { return mapCentralInputURL(value, company, central) }, false)
			if e != nil {
				return e
			}
			options[key] = mapped
		}
	}
	f["options"], _ = json.Marshal(options)
	return nil
}
func mapCentralInputURL(raw, company, central string) (string, error) {
	if raw == "" {
		return raw, nil
	}
	u, e := url.Parse(raw)
	if e != nil {
		return raw, nil
	}
	if u.IsAbs() && u.Scheme+"://"+u.Host != central {
		return raw, nil
	}
	if !strings.HasPrefix(u.Path, "/references/") && !strings.HasPrefix(u.Path, "/template-media/") && !strings.HasPrefix(u.Path, "/media/") {
		return raw, nil
	}
	return mapMediaURL(raw, central, company, func(string, url.Values) bool { return true })
}
