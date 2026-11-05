// Package requestjson describes rejected JSON requests without disclosing input
// values, arbitrary field names, or implementation types. Callers own envelopes.
package requestjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

type Problem struct {
	Message string
	Param   string
}

// Decode preserves encoding/json's accepted values (including null, duplicate
// keys and case-insensitive fields), the caller's byte limit, and one-value rule.
func Decode(w http.ResponseWriter, r *http.Request, target any, limit int64, strict bool) *Problem {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	// A bounded prefix permits an exact position for an unknown key without
	// retaining an arbitrarily large body. It is never included in a response.
	var prefix bodyPrefix
	var reader io.Reader = r.Body
	if strict {
		reader = io.TeeReader(reader, &prefix)
	}
	d := json.NewDecoder(reader)
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(target); err != nil {
		p := describe(err, target, true)
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			if at := unknownKeyPosition(prefix.data, err); at > 0 {
				p.Message = fmt.Sprintf("Unsupported JSON field ending at byte %d. Remove that field or correct its spelling using the endpoint documentation.", at)
			}
		}
		return &p
	}
	end := d.InputOffset()
	if err := d.Decode(&struct{}{}); err != io.EOF {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			p := describe(err, target, true)
			return &p
		}
		return &Problem{Message: fmt.Sprintf("Unexpected content after the first JSON value (which ends at byte %d). Remove trailing content and send exactly one JSON object.", end)}
	}
	return nil
}

type bodyPrefix struct{ data []byte }

func (p *bodyPrefix) Write(b []byte) (int, error) {
	const maxPrefix = 8192
	if len(p.data) < maxPrefix {
		p.data = append(p.data, b[:min(len(b), maxPrefix-len(p.data))]...)
	}
	return len(b), nil
}

// Report a position only when the complete captured JSON has exactly one key
// matching the decoder's unknown key. Repeated keys or a truncated prefix make
// the location ambiguous; neither request keys nor values become public text.
func unknownKeyPosition(body []byte, err error) int64 {
	if !json.Valid(body) {
		return 0
	}
	name, unquoteErr := strconv.Unquote(strings.TrimPrefix(err.Error(), "json: unknown field "))
	if unquoteErr != nil {
		return 0
	}
	type frame struct{ object, key bool }
	var stack []frame
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var at int64
	count := 0
	for {
		token, err := d.Token()
		if err != nil {
			break
		}
		if c, ok := token.(json.Delim); ok {
			if c == '}' || c == ']' {
				stack = stack[:len(stack)-1]
			} else {
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].key = true
				}
				stack = append(stack, frame{object: c == '{', key: true})
			}
		} else if len(stack) > 0 && stack[len(stack)-1].object {
			f := &stack[len(stack)-1]
			if f.key && token == name {
				at = d.InputOffset()
				count++
			}
			f.key = !f.key
		}
	}
	if count != 1 {
		return 0
	}
	return at
}

// Describe uses typed decoder metadata and schema-declared JSON names only.
// It omits byte positions for callers decoding reconstructed field objects.
// In particular, err.Error(), UnmarshalTypeError.Value and unknown field names
// must never become public text: all can contain credentials or input fragments.
func Describe(err error, target any) Problem {
	return describe(err, target, false)
}

func describe(err error, target any, positions bool) Problem {
	var size *http.MaxBytesError
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &size):
		return Problem{Message: fmt.Sprintf("Request body exceeds the %d bytes limit. Reduce the body to at most %d bytes and send one JSON object.", size.Limit, size.Limit)}
	case errors.Is(err, io.EOF):
		return Problem{Message: "Request body is empty. Send one JSON object with the documented fields."}
	case errors.Is(err, io.ErrUnexpectedEOF):
		return Problem{Message: "Unexpected end of JSON. Complete the JSON object, including closing quotes and brackets, and resend the complete body."}
	case errors.As(err, &syntax):
		if !positions {
			return Problem{Message: "Malformed JSON field. Correct the JSON syntax and send one complete JSON object."}
		}
		return Problem{Message: fmt.Sprintf("Malformed JSON at byte %d. Correct the JSON syntax at that position and send one complete JSON object.", syntax.Offset)}
	case errors.As(err, &typ):
		param := publicField(reflect.TypeOf(target), typ.Field, typ.Type)
		// Custom unmarshallers can return offsets relative to a field's raw
		// value. A schema path is useful without claiming an original offset.
		return Problem{Param: param, Message: fmt.Sprintf("JSON value must be %s. Correct the value%s and resend the request.", jsonShape(typ.Type), fieldHint(param))}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return Problem{Message: "Unsupported JSON field. Remove fields not listed in the endpoint documentation or correct their spelling."}
	default:
		// Custom unmarshallers and I/O errors may contain arbitrary private text.
		return Problem{Message: "Invalid JSON field value. Check the documented field types and formats, then send one complete JSON object."}
	}
}

func fieldHint(param string) string {
	if param == "" {
		return " using the documented field types"
	}
	return " for " + param
}

func indirect(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func publicField(t reflect.Type, field string, expected reflect.Type) string {
	if field == "" {
		return ""
	}
	path := ""
	parts := strings.Split(field, ".")
	for i := 0; i < len(parts); {
		t = indirect(t)
		for t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			path += "[]"
			t = indirect(t.Elem())
			// Newer Go decoders include array indices in Field; older ones
			// omit them. Never copy an index or dynamic key into the path.
			if i < len(parts) && arrayIndex(parts[i]) {
				i++
			}
		}
		if i == len(parts) {
			break
		}
		if t == nil || t.Kind() != reflect.Struct {
			return ""
		}
		var found reflect.Type
		for fieldIndex := 0; fieldIndex < t.NumField(); fieldIndex++ {
			f := t.Field(fieldIndex)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.IsExported() && name != "" && name != "-" && name == parts[i] {
				found = f.Type
				if path != "" {
					path += "."
				}
				path += name
				break
			}
		}
		if found == nil || len(path) > 160 {
			return ""
		}
		t = found
		i++
	}
	for t = indirect(t); t != nil && t != expected && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array); t = indirect(t.Elem()) {
		path += "[]"
	}
	return path
}

func arrayIndex(part string) bool {
	if part == "" || len(part) > 20 {
		return false
	}
	for _, c := range part {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func jsonShape(t reflect.Type) string {
	t = indirect(t)
	if t == nil {
		return "a value of the documented JSON type"
	}
	if t == reflect.TypeFor[json.Number]() {
		return "a JSON number"
	}
	switch t.Kind() {
	case reflect.String:
		return "a JSON string"
	case reflect.Bool:
		return "a JSON boolean (true or false)"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		max := int64(1)<<(bits-1) - 1
		return fmt.Sprintf("a JSON integer from %d to %d", -max-1, max)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("a JSON integer from 0 to %d", ^uint64(0)>>(64-t.Bits()))
	case reflect.Float32, reflect.Float64:
		return "a JSON number within the supported numeric range"
	case reflect.Struct, reflect.Map:
		return "a JSON object"
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return "a base64 JSON string or an array of byte integers"
		}
		return "a JSON array"
	default:
		return "a value of the documented JSON type"
	}
}
