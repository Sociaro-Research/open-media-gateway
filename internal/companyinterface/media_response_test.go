package companyinterface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCompanyMediaTypedURLBindingAndInputRoundtrip(t *testing.T) {
	company, id := uuid.NewString(), uuid.NewString()
	central, local := "https://central.example", "https://secondary.example"
	signature := strings.Repeat("a", 64)
	original := "/references/" + company + "/" + id + "?signature=" + signature
	input := []byte(`{"data":[{"id":"` + id + `","url":"` + original + `","name":"` + original + `"}]}`)
	c := Classify("GET", "/v1/references")
	mapped, e := mediaJSONResponse(c, 200, "GET", "/v1/references", company, central, input)
	if e != nil {
		t.Fatal(e)
	}
	var f struct{ Data []struct{ URL, Name string } }
	json.Unmarshal(mapped, &f)
	if f.Data[0].URL != LocalCustomerPrefix+strings.TrimPrefix(original, "/") || f.Data[0].Name != original {
		t.Fatal("mapping touched unrelated text", string(mapped))
	}
	body := []byte(`{"model":"mock/image","prompt":"` + f.Data[0].URL + `","image":{"image_url":"` + local + f.Data[0].URL + `"},"options":{"parameters":{"arbitrary":"` + f.Data[0].URL + `"}}}`)
	restored, e := restoreMediaRequest(Classify("POST", "/v1/generations"), body, company, central, local)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(restored, []byte(central+original)) || !bytes.Contains(restored, []byte(`"prompt":"`+f.Data[0].URL+`"`)) || !bytes.Contains(restored, []byte(`"arbitrary":"`+f.Data[0].URL+`"`)) {
		t.Fatal("typed input changed unrelated fields", string(restored))
	}
	for _, bad := range []string{strings.Replace(original, company, uuid.NewString(), 1), strings.Replace(original, id, uuid.NewString(), 1), "https://foreign.example" + original, original + "&signature=" + signature, strings.Replace(original, "/references/", "/%72eferences/", 1), original + "#fragment"} {
		raw, _ := json.Marshal(map[string]any{"data": []any{map[string]string{"id": id, "url": bad}}})
		if _, e := mediaJSONResponse(c, 200, "GET", "/v1/references", company, central, raw); e == nil {
			t.Fatal("accepted foreign/mismatched reference", bad)
		}
	}
	for _, bad := range []string{"https://foreign.example" + f.Data[0].URL, local + strings.Replace(f.Data[0].URL, company, uuid.NewString(), 1), local + f.Data[0].URL + "&signature=" + signature, local + LocalCustomerPrefix + "v1/system/templates", local + strings.Replace(f.Data[0].URL, "references/", "references%2f", 1)} {
		if _, e := restoreMediaInput(bad, company, central, local); e == nil {
			t.Fatal("accepted substituted facade input", bad)
		}
	}
}
func TestCompanyMediaGenerationResponseObjectBinding(t *testing.T) {
	company, id := uuid.NewString(), uuid.NewString()
	path := "/v1/generations/" + id
	base := map[string]any{"id": id, "object": "generation", "status_url": path, "content_url": path + "/content", "prompt": "https://foreign.example/do-not-rewrite"}
	c := Classify("GET", path)
	for _, bad := range []string{"https://foreign.example" + path + "/content", path + "/content?download=1", "/v1/generations/" + uuid.NewString() + "/content", strings.Replace(path, "/generations/", "/generations%2f", 1) + "/content"} {
		base["content_url"] = bad
		raw, _ := json.Marshal(base)
		if _, e := mediaJSONResponse(c, 200, "GET", path, company, "https://central.example", raw); e == nil {
			t.Fatal("accepted wrong typed content", bad)
		}
	}
}

func TestCompanyMediaPrestreamErrorsAreBoundedAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		retry  string
		want   error
	}{
		{403, "company_interface_denied", "", ErrDenied}, {409, "company_interface_conflict", "", ErrConflict}, {409, "company_interface_update_required", "", ErrProtocol}, {428, "company_interface_pending", "", ErrPending}, {429, "company_interface_rate_limit", "60", ErrRateLimited}, {429, "company_interface_rate_limit", "https://attacker.example", ErrUnavailable}, {503, "company_interface_unavailable", "", ErrUnavailable}, {403, "foreign-secret-code", "", ErrUnavailable}, {401, "company_interface_denied", "", ErrUnavailable}, {404, "not_found", "", ErrUnavailable},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			raw := `{"error":{"code":"` + tc.code + `","message":"private upstream evidence"}}`
			if method == "HEAD" {
				raw = ""
			}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(raw))}
			if tc.retry != "" {
				response.Header.Set("Retry-After", tc.retry)
			}
			want := tc.want
			if method == "HEAD" && tc.status == 403 {
				want = ErrDenied
			}
			if method == "HEAD" && tc.status == 409 {
				want = ErrConflict
			}
			e := readMediaProtocolError(context.Background(), response, method)
			if !errors.Is(e, want) || strings.Contains(e.Error(), "private upstream") {
				t.Fatal("unsafe protocol error", tc, method, e)
			}
		}
	}
	response := &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", MaxProtocolBody+1)))}
	if e := readMediaProtocolError(context.Background(), response, "GET"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("oversized unframed error", e)
	}
}
