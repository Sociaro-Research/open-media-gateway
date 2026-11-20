package minimax

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// A financial assertion uses the reviewed JSON spelling and unique members.
// A missing member, a newly shaped envelope, or competing values leave billing
// unknown. Each object is small; the enclosing error document is capped below.
func h3ErrorObject(raw json.RawMessage, keys ...string) map[string]json.RawMessage {
	d := json.NewDecoder(bytes.NewReader(raw))
	if open, err := d.Token(); err != nil || open != json.Delim('{') {
		return nil
	}
	obj := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains(keys, key) || obj[key] != nil {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		obj[key] = value
	}
	if close, err := d.Token(); err != nil || close != json.Delim('}') {
		return nil
	}
	if _, err := d.Token(); err != io.EOF {
		return nil
	}
	return obj
}

func h3ErrorString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func h3RejectionProvesNoCharge(raw []byte, status, count int) bool {
	if len(raw) > 256<<10 || count < 1 || count > 10 {
		return false
	}
	var wantType string
	switch status {
	case http.StatusPaymentRequired:
		wantType = "insufficient_balance_error"
	case http.StatusUnprocessableEntity:
		wantType = "unprocessable_entity_error"
	case http.StatusInternalServerError:
		wantType = "server_error"
	default:
		return false
	}
	envelope := h3ErrorObject(raw, "type", "error", "created", "data", "usage", "request_id")
	var created int64
	if h3ErrorString(envelope["type"]) != "error" || json.Unmarshal(envelope["created"], &created) != nil || created <= 0 {
		return false
	}
	detail := h3ErrorObject(envelope["error"], "type", "http_code", "message")
	if h3ErrorString(detail["type"]) != wantType || h3ErrorString(detail["http_code"]) != strconv.Itoa(status) {
		return false
	}
	if detail["message"] != nil && strings.TrimSpace(h3ErrorString(detail["message"])) == "" || envelope["request_id"] != nil && h3ErrorString(envelope["request_id"]) == "" {
		return false
	}
	var slots []json.RawMessage
	if json.Unmarshal(envelope["data"], &slots) != nil || len(slots) != count {
		return false
	}
	var safety, balance int
	for _, slot := range slots {
		item := h3ErrorObject(slot, "error")
		cause := h3ErrorObject(item["error"], "code", "message")
		code := h3ErrorString(cause["code"])
		number, err := strconv.ParseUint(code, 10, 32)
		if err != nil || number == 0 || strconv.FormatUint(number, 10) != code || cause["message"] != nil && strings.TrimSpace(h3ErrorString(cause["message"])) == "" {
			return false
		}
		switch code {
		case "1026", "1027":
			safety++
		case "1008", "2053", "2067":
			balance++
		}
	}
	if status == 422 && safety != count || status == 402 && balance != count || status == 500 && (safety == count || balance == count) {
		return false
	}
	usage := h3ErrorObject(envelope["usage"], "generated_images", "input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details")
	in := h3ErrorObject(usage["input_tokens_details"], "text_tokens", "image_tokens")
	out := h3ErrorObject(usage["output_tokens_details"], "image_tokens")
	for _, counter := range []json.RawMessage{usage["generated_images"], in["text_tokens"], in["image_tokens"], out["image_tokens"]} {
		if string(counter) != "0" {
			return false
		}
	}
	for _, key := range []string{"total_tokens", "input_tokens", "output_tokens"} {
		if counter := usage[key]; counter != nil && string(counter) != "0" {
			return false
		}
	}
	return true
}
