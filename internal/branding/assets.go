package branding

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"regexp"
	"strings"
)

// SVG uses a small passive subset. References may only target local fragments;
// scripts, stylesheets, links, animation, foreign HTML and external resources fail.
var svgElements = strings.Fields("svg g path rect circle ellipse line polyline polygon defs linearGradient radialGradient stop clipPath mask title desc text tspan use symbol")
var svgAttributes = strings.Fields("id viewBox width height x y x1 x2 y1 y2 cx cy r rx ry d points fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit stroke-opacity opacity transform preserveAspectRatio gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity clip-path clip-rule mask maskUnits maskContentUnits font-family font-size font-weight text-anchor dominant-baseline dx dy href xmlns version")
var svgFragment = regexp.MustCompile(`^#[a-zA-Z0-9_-]+$`)
var svgURL = regexp.MustCompile(`(?i)url\(\s*['"]?#[a-zA-Z0-9_-]+['"]?\s*\)`)

func contains(list []string, value string) bool {
	for _, v := range list {
		if value == v {
			return true
		}
	}
	return false
}
func (a Asset) Validate(key string) error {
	if len(a.Data) == 0 || len(a.Data) > MaxAssetBytes {
		return errors.New("image must be at most ≈524.29 KB (524288 bytes)")
	}
	if a.MIME == "image/svg+xml" {
		if key == "touch_icon" || key == "email_logo" {
			return errors.New("use PNG or JPEG for this image")
		}
		d := xml.NewDecoder(bytes.NewReader(a.Data))
		depth, roots := 0, 0
		for {
			token, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errors.New("invalid SVG")
			}
			switch t := token.(type) {
			case xml.StartElement:
				if depth == 0 {
					roots++
					if t.Name.Local != "svg" {
						return errors.New("SVG root required")
					}
				}
				depth++
				if depth > 64 || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") || !contains(svgElements, t.Name.Local) {
					return errors.New("SVG must contain only passive vector shapes")
				}
				for _, attr := range t.Attr {
					if attr.Name.Local == "xmlns" || attr.Name.Space == "xmlns" {
						if attr.Value != "http://www.w3.org/2000/svg" && attr.Value != "http://www.w3.org/1999/xlink" {
							return errors.New("unsupported SVG namespace")
						}
						continue
					}
					if (attr.Name.Space != "" && attr.Name.Space != "http://www.w3.org/1999/xlink") || !contains(svgAttributes, attr.Name.Local) {
						return errors.New("unsupported SVG attribute; export a plain SVG without styles")
					}
					v := strings.TrimSpace(attr.Value)
					if strings.Contains(v, `\`) {
						return errors.New("SVG attribute escapes are not supported")
					}
					if attr.Name.Local == "href" && !svgFragment.MatchString(v) {
						return errors.New("SVG references must be local")
					}
					if strings.Contains(strings.ToLower(v), "url") && (!svgURL.MatchString(v) || svgURL.ReplaceAllString(v, "") != "") {
						return errors.New("external SVG resources are not allowed")
					}
				}
			case xml.EndElement:
				depth--
			case xml.Directive:
				return errors.New("SVG directives are not supported")
			case xml.ProcInst:
				if t.Target != "xml" {
					return errors.New("SVG processing instructions are not supported")
				}
			case xml.CharData:
				if depth == 0 && strings.TrimSpace(string(t)) != "" {
					return errors.New("invalid SVG content")
				}
			}
		}
		if roots != 1 || depth != 0 {
			return errors.New("invalid SVG document")
		}
		return nil
	}
	if a.MIME != "image/png" && a.MIME != "image/jpeg" {
		return errors.New("use PNG, JPEG or plain SVG")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(a.Data))
	if err != nil || (a.MIME == "image/png" && format != "png") || (a.MIME == "image/jpeg" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width*cfg.Height > 8000000 {
		return errors.New("invalid image or dimensions exceed 4096 pixels / 8 megapixels")
	}
	// Decode once during upload to reject truncated files; dimensions bound memory.
	if _, _, err = image.Decode(bytes.NewReader(a.Data)); err != nil {
		return errors.New("image could not be decoded")
	}
	if key == "touch_icon" && cfg.Width != cfg.Height {
		return errors.New("home screen icon must be square")
	}
	return nil
}
