package companyinterface

import (
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

const MediaTimeout = time.Hour
const MediaIdleTimeout = 30 * time.Second

func mediaCustomerContract(method, path string) Contract {
	if method != "GET" && method != "HEAD" || strings.ContainsAny(path, "%\\?#\r\n\x00") || strings.Contains(path, "//") {
		return Contract{Class: Disallowed}
	}
	parts := strings.Split(path, "/")
	binary := func(id, query string, management bool) Contract {
		c := customerContract(id, "", query, "media-bytes", "generation", management)
		c.Binary = true
		c.Stream = true
		c.Timeout = MediaTimeout
		return c
	}
	if len(parts) == 3 && validID(parts[2]) {
		switch parts[1] {
		case "media":
			return binary("signed-generation-media", "expires signature artifact download", false)
		case "template-media":
			return binary("signed-template-media", "signature", false)
		}
	}
	if len(parts) == 4 && parts[1] == "references" && validID(parts[2]) && validID(parts[3]) {
		return binary("signed-reference-media", "signature", false)
	}
	if len(parts) == 5 && parts[1] == "provider-assets" && validID(parts[2]) && mediaToken(parts[3]) && parts[4] == "icon.png" {
		return binary("provider-icon", "v organization", false)
	}
	if len(parts) == 6 && parts[1] == "v1" && parts[2] == "system" && parts[3] == "template-references" && validID(parts[4]) && parts[5] == "content" {
		return binary("shared-template-content", "", false)
	}
	if len(parts) >= 5 && parts[1] == "v1" && (parts[2] == "generations" || parts[2] == "videos") && validID(parts[3]) {
		if len(parts) == 5 {
			switch parts[4] {
			case "content", "last-frame":
				if parts[2] == "videos" && parts[4] != "content" {
					break
				}
				c := binary("generation-"+parts[4], "download", false)
				c.Auth = []AuthKind{CentralKey}
				return c
			case "media-access":
				if parts[2] == "generations" {
					c := customerContract("generation-media-access", "", "", "media-access", "generation", false)
					c.Auth = []AuthKind{CentralKey}
					return c
				}
			}
		}
		if len(parts) == 7 && parts[2] == "generations" && parts[4] == "artifacts" && core.ValidGeneratedArtifactID(parts[5]) && parts[6] == "content" {
			c := binary("generation-artifact-content", "download", false)
			c.Auth = []AuthKind{CentralKey}
			return c
		}
	}
	if len(parts) >= 6 && parts[1] == "v1" && parts[2] == "organizations" && validID(parts[3]) && validID(parts[5]) {
		switch parts[4] {
		case "reports":
			if len(parts) == 6 {
				c := customerContract("company-report-detail", "", "", "report-detail", "generation", true)
				if method == "HEAD" {
					c.Scope = "organizations:manage"
				}
				return c
			}
			if len(parts) == 7 && (parts[6] == "content" || parts[6] == "last-frame") {
				return binary("company-report-"+parts[6], "download", true)
			}
		case "generations":
			if len(parts) == 7 {
				switch parts[6] {
				case "content", "last-frame":
					return binary("company-generation-"+parts[6], "download", false)
				case "media-access":
					c := customerContract("company-generation-media-access", "", "", "media-access", "generation", false)
					if method == "HEAD" {
						c.Scope = "organizations:manage"
					}
					return c
				}
			}
		}
	}
	return Contract{Class: Disallowed}
}
func mediaToken(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func mediaQueryContract(c Contract, path string, q url.Values, company string) error {
	if v, ok := q["download"]; ok && (len(v) != 1 || v[0] != "1") {
		return ErrDenied
	}
	if c.ID == "provider-icon" {
		if v := q.Get("organization"); v != "" && v != company {
			return ErrDenied
		}
		if len(q.Get("v")) > 256 {
			return ErrDenied
		}
	}
	if c.ID != "signed-generation-media" && c.ID != "signed-reference-media" && c.ID != "signed-template-media" {
		return nil
	}
	signature := q.Get("signature")
	decoded, e := hex.DecodeString(signature)
	if e != nil || len(decoded) != 32 || len(signature) != 64 {
		return ErrDenied
	}
	if c.ID == "signed-reference-media" {
		owner := strings.Split(path, "/")[2]
		if owner != company && owner != templates.LibraryOwnerID {
			return ErrDenied
		}
	}
	if c.ID == "signed-generation-media" {
		expires, e := strconv.ParseInt(q.Get("expires"), 10, 64)
		if e != nil || expires <= 0 {
			return ErrDenied
		}
		if a := q.Get("artifact"); a != "" && !core.ValidGeneratedArtifactID(a) {
			return ErrDenied
		}
	}
	return nil
}
