package minimax

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"net/http"
	"strconv"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type h3ImageItem struct {
	URL    string `json:"url"`
	Base64 string `json:"b64_json"`
	Size   string `json:"size"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type h3ImageEnvelope struct {
	Data   []json.RawMessage `json:"data"`
	Format string            `json:"output_format"`
	Usage  json.RawMessage   `json:"usage"`
	Type   string            `json:"type"`
	Error  *struct {
		HTTPCode string `json:"http_code"`
	} `json:"error"`
}

type h3TokenReport struct {
	Generated *int64 `json:"generated_images"`
	Total     *int64 `json:"total_tokens"`
	Input     *int64 `json:"input_tokens"`
	Output    *int64 `json:"output_tokens"`
	In        struct {
		Text  *int64 `json:"text_tokens"`
		Image *int64 `json:"image_tokens"`
	} `json:"input_tokens_details"`
	Out struct {
		Image *int64 `json:"image_tokens"`
	} `json:"output_tokens_details"`
}

func h3Usage(raw json.RawMessage, success int, edit bool) *core.ProviderUsage {
	var v h3TokenReport
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	for _, n := range []*int64{v.Generated, v.Total, v.Input, v.Output, v.In.Text, v.In.Image, v.Out.Image} {
		if n != nil && (*n < 0 || *n > 1<<53-1) {
			return nil
		}
	}
	// Missing details contribute a lower bound. Complete details must sum
	// exactly to any independently reported aggregate.
	quantity := func(n *int64) int64 {
		if n == nil {
			return 0
		}
		return *n
	}
	input := quantity(v.In.Text) + quantity(v.In.Image)
	inputKnown := v.In.Text != nil && v.In.Image != nil
	if v.Input != nil {
		if *v.Input < input || inputKnown && *v.Input != input {
			return nil
		}
		input, inputKnown = *v.Input, true
	}
	output := v.Out.Image
	if v.Output != nil {
		if output != nil && *output != *v.Output {
			return nil
		}
		output = v.Output
	}
	minimumTotal := input + quantity(output)
	if v.Total != nil && (*v.Total < minimumTotal || inputKnown && output != nil && *v.Total != minimumTotal) ||
		v.Generated != nil && *v.Generated != int64(success) || !edit && v.In.Image != nil && *v.In.Image != 0 {
		return nil
	}
	var usage core.ProviderUsage
	// Enhanced-prompt text is already part of this counter. Aggregate input,
	// aggregate output, revised prompts and the raw provider response stay private.
	for _, component := range []struct {
		name string
		n    *int64
	}{{"text_input_tokens", v.In.Text}, {"image_input_tokens", v.In.Image}, {"image_output_tokens", v.Out.Image}} {
		if component.n != nil {
			usage.Metrics = append(usage.Metrics, core.UsageMetric{Name: component.name, Unit: "token", Quantity: float64(*component.n)})
		}
	}
	if len(usage.Metrics) == 0 {
		return nil
	}
	return &usage
}

func (a *h3Images) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if a.requestConnectionScope(ctx) == "" {
		return core.ProviderTask{}, errors.New("MiniMax provider connection identity is not bound")
	}
	path, raw, err := h3ImageRequest(r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	if budget := r.Options.TimeoutSeconds; budget != nil {
		ctx = httpclient.WithRequestTimeout(ctx, time.Duration(*budget)*time.Second)
	}
	data, err := a.do(ctx, http.MethodPost, path, bytes.NewReader(raw), "application/json")
	n := max(1, r.Options.OutputCount)
	edit := path == "/v1/images/edits"
	var out h3ImageEnvelope
	if err != nil {
		var rejected *APIError
		if !errors.As(err, &rejected) {
			return core.ProviderTask{}, err
		}
		if rejected.Status != 402 && rejected.Status != 422 && rejected.Status != 500 {
			return core.ProviderTask{}, err
		}
		if h3RejectionProvesNoCharge(data, rejected.Status, n) {
			return core.ProviderTask{}, core.UnchargedImageRejection{}
		}
		if rejected.Status == 500 {
			return core.ProviderTask{}, core.ErrProviderSubmissionUncertain
		}
		return core.ProviderTask{}, err
	}
	if json.Unmarshal(data, &out) != nil || out.Error != nil || out.Type == "error" || len(out.Data) != n {
		return core.ProviderTask{}, core.ErrProviderSubmissionUncertain
	}
	format := r.Options.Format
	if format == "" || format == "jpg" {
		format = "jpeg"
	}
	if out.Format != format {
		return core.ProviderTask{}, core.ErrProviderSubmissionUncertain
	}
	inline := r.Options.Parameters["response_format"] == "b64_json"
	var result core.ProviderTask
	complete := true
	for i, rawItem := range out.Data {
		var item h3ImageItem
		if json.Unmarshal(rawItem, &item) != nil {
			complete = false
			continue
		}
		if item.Error != nil {
			if item.Error.Code == "" || item.URL != "" || item.Base64 != "" {
				complete = false
			}
			continue
		}
		if requested, _ := r.Options.Parameters["size"].(string); requested != "" && requested != "auto" && item.Size != requested {
			complete = false
			continue
		}
		artifact, ok := item.artifact(i, format, inline)
		if !ok {
			complete = false
			continue
		}
		if !inline {
			artifact.SourceReference = a.sourceReference(ctx, h3ImageSource{URL: artifact.URL, Format: format, Size: item.Size})
			artifact.URL = ""
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	if len(result.Artifacts) == 0 {
		return core.ProviderTask{}, core.ErrProviderSubmissionUncertain
	}
	if complete {
		result.Usage = h3Usage(out.Usage, len(result.Artifacts), edit)
	}
	if core.ValidateAcceptedProviderTask(result) != nil {
		return core.ProviderTask{}, core.ErrProviderSubmissionUncertain
	}
	return result, nil
}

func (item h3ImageItem) artifact(index int, format string, inline bool) (core.ProviderArtifact, bool) {
	x, y, ok := h3Pair(item.Size, h3Dimensions)
	if !ok || x*y < 655360 || x*y > 20000000 {
		return core.ProviderArtifact{}, false
	}
	source := item.URL
	if inline {
		if item.Base64 == "" || item.URL != "" {
			return core.ProviderArtifact{}, false
		}
		decoded, err := base64.StdEncoding.DecodeString(item.Base64)
		contentType := "image/" + format
		if err != nil || http.DetectContentType(decoded) != contentType {
			return core.ProviderArtifact{}, false
		}
		info, err := mediafile.Inspect(decoded, contentType)
		if err != nil || int64(info.Width) != x || int64(info.Height) != y {
			return core.ProviderArtifact{}, false
		}
		source = "data:image/" + format + ";base64," + item.Base64
	} else if !publicURL(source) || item.Base64 != "" {
		return core.ProviderArtifact{}, false
	}
	a := core.ProviderArtifact{ID: "image-" + strconv.Itoa(index+1), MediaType: "image", Format: format, URL: source}
	// Verify each source independently so a malformed sibling cannot erase
	// another acknowledged image. The combined envelope is bounded separately.
	return a, core.ValidateAcceptedProviderTask(core.ProviderTask{Artifacts: []core.ProviderArtifact{a}}) == nil
}
