package companyinterface

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Only named media-input fields may translate a local facade URL back to its
// fixed central signed resource. Prompt text, webhook URLs, provider parameters,
// template snapshots and arbitrary user JSON are never traversed or rewritten.
func restoreMediaInput(raw, company, central, local string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return raw, nil
	} // The original central input validator owns unrelated URLs.
	if !strings.HasPrefix(u.Path, LocalCustomerPrefix) {
		return raw, nil
	}
	if u.User != nil || u.Fragment != "" || u.Opaque != "" || u.EscapedPath() != u.Path || u.Host != "" && u.Scheme+"://"+u.Host != local || u.Host == "" && !strings.HasPrefix(raw, "/") {
		return "", ErrDenied
	}
	path := "/" + strings.TrimPrefix(u.Path, LocalCustomerPrefix)
	c := Classify("GET", path)
	if c.ID != "signed-reference-media" && c.ID != "signed-template-media" && c.ID != "signed-generation-media" {
		return "", ErrDenied
	}
	candidate := path
	if u.RawQuery != "" {
		candidate += "?" + u.RawQuery
	}
	if _, _, e = mediaPath(candidate, central, company); e != nil {
		return "", ErrDenied
	}
	return central + candidate, nil
}
func transformMediaValue(raw json.RawMessage, transform func(string) (string, error), objects bool) (json.RawMessage, error) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		mapped, e := transform(value)
		if e != nil {
			return nil, e
		}
		return json.Marshal(mapped)
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && array != nil {
		for i, v := range array {
			var nested []json.RawMessage
			if json.Unmarshal(v, &nested) == nil {
				return nil, ErrDenied
			}
			mapped, e := transformMediaValue(v, transform, objects)
			if e != nil {
				return nil, e
			}
			array[i] = mapped
		}
		return json.Marshal(array)
	}
	if objects {
		var object mediaFields
		if json.Unmarshal(raw, &object) == nil && len(object) == 1 {
			if v, ok := object["image_url"]; ok {
				var value string
				if json.Unmarshal(v, &value) != nil {
					return nil, ErrDenied
				}
				mapped, e := transform(value)
				if e != nil {
					return nil, e
				}
				object["image_url"], _ = json.Marshal(mapped)
				return json.Marshal(object)
			}
		}
	}
	return raw, nil // Preserve original handler error semantics for unsupported shapes/file_id.
}
func restoreMediaRequest(c Contract, raw []byte, company, central, local string) ([]byte, error) {
	if c.RequestBody != "generation-json" {
		return raw, nil
	}
	var fields mediaFields
	if json.Unmarshal(raw, &fields) != nil {
		return nil, ErrDenied
	}
	transform := func(value string) (string, error) { return restoreMediaInput(value, company, central, local) }
	for _, key := range []string{"image", "input_reference"} {
		if value, ok := fields[key]; ok {
			mapped, e := transformMediaValue(value, transform, true)
			if e != nil {
				return nil, e
			}
			fields[key] = mapped
		}
	}
	if value, ok := fields["options"]; ok {
		var options mediaFields
		if json.Unmarshal(value, &options) == nil && options != nil {
			for _, key := range []string{"image_urls", "video_url", "video_urls", "audio_url", "audio_urls"} {
				if v, ok := options[key]; ok {
					mapped, e := transformMediaValue(v, transform, false)
					if e != nil {
						return nil, e
					}
					options[key] = mapped
				}
			}
			fields["options"], _ = json.Marshal(options)
		}
	}
	return json.Marshal(fields)
}
