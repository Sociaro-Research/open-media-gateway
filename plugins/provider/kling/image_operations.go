package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	ImageGeneration3  = "kling-v3-image-generation"
	ImageGeneration21 = "kling-v2-1-image-generation"
	ImageOmni3        = "kling-v3-omni-image-generation"
	ImageO1           = "kling-image-o1-generation"
	MultiImage21      = "kling-v2-1-multi-image-to-image"
	ImageOutpainting  = "kling-image-outpainting"
	ImageMultiShot    = "kling-ai-multi-shot"
)

// Operation IDs distinguish reviewed routes; modelName is always the documented
// native enum. General editing endpoints do not accept a model selector.
type imageOperation struct {
	id, modelName, path, label                                string
	minImages, maxImages                                      int
	rate                                                      money.USD
	omni, series, negative, optionalPrompt, noPrompt, perCall bool
}

func imageOperations() []imageOperation {
	return []imageOperation{
		{id: ImageGeneration3, modelName: "kling-v3", path: "/v1/images/generations", label: "Kling Image 3.0", maxImages: 1, rate: 28000, negative: true},
		{id: ImageGeneration21, modelName: "kling-v2-1", path: "/v1/images/generations", label: "Kling Image 2.1", maxImages: 1, rate: 14000, negative: true},
		{id: ImageOmni3, modelName: "kling-v3-omni", path: "/v1/images/omni-image", label: "Kling Image 3.0 Omni", maxImages: 10, rate: 28000, omni: true, series: true},
		{id: ImageO1, modelName: "kling-image-o1", path: "/v1/images/omni-image", label: "Kling Image O1", maxImages: 10, rate: 28000, omni: true},
		{id: MultiImage21, modelName: "kling-v2-1", path: "/v1/images/multi-image2image", label: "Kling Image 2.1 · Multi-image", minImages: 1, maxImages: 6, rate: 56000, optionalPrompt: true},
		{id: ImageOutpainting, path: "/v1/images/editing/expand", label: "Kling · Outpainting", minImages: 1, maxImages: 1, rate: 28000, optionalPrompt: true},
		{id: ImageMultiShot, path: "/v1/general/ai-multi-shot", label: "Kling · AI Multi-Shot", minImages: 1, maxImages: 1, rate: 70000, noPrompt: true, perCall: true},
	}
}
func findImageOperation(id string) (imageOperation, bool) {
	for _, op := range imageOperations() {
		if op.id == id {
			return op, true
		}
	}
	return imageOperation{}, false
}
func isImageOperation(id string) bool { _, ok := findImageOperation(id); return ok }
func imageBound(v float64) *float64   { return &v }
func imageModel(op imageOperation) core.MediaModel {
	m := core.MediaModel{ID: op.id, DisplayName: op.label, MediaType: "image", MaxPromptCharacters: 2500, NoSeed: true, NoLanguage: true, NoInstructions: true, MaxOutputs: 9, SupportsImageInput: true, RequiresImageInput: op.minImages > 0, MinImageInputs: op.minImages, MaxImageInputs: op.maxImages, SupportsNegativePrompt: op.negative, PromptOptional: op.optionalPrompt, NoPrompt: op.noPrompt,
		ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: op.maxImages, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 10 << 20, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}},
		Pricing:              core.MediaModelPricing{Known: true, ReferencePriceUSD: float64(op.rate) / 1e6, ReferenceDescription: "Published USD balance list price estimate for one image; account discounts, resource packs and actual deductions remain unverified."},
	}
	if op.id == ImageGeneration3 || op.omni {
		m.MaxReferenceInputs = 10
		m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "element", MaxCount: 10, Subtypes: []string{"multi_image"}}}
	}
	m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "References", MinImages: op.minImages, MaxImages: op.maxImages}}
	m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference"}, Default: "reference"}}
	if op.modelName != "" {
		m.Ratios = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3", "21:9"}
	}
	if op.id == ImageGeneration3 || op.id == ImageGeneration21 || op.omni {
		m.Resolutions = []string{"1k", "2k"}
	}
	if op.omni {
		m.Ratios = append(m.Ratios, "auto")
	}
	if op.id == ImageOmni3 {
		m.Resolutions = append(m.Resolutions, "4k")
	}
	if op.series {
		m.Parameters = append(m.Parameters, core.ParameterField{Key: "result_type", Label: "Result type", Type: "select", Default: "single", Choices: []string{"single", "series"}}, core.ParameterField{Key: "series_amount", Label: "Images in series", Type: "select", Choices: []string{"2", "3", "4", "5", "6", "7", "8", "9", "auto"}})
	}
	if op.id == ImageGeneration21 {
		m.Parameters = append(m.Parameters, core.ParameterField{Key: "image_reference", Label: "Reference type", Type: "select", Choices: []string{"subject", "face"}}, core.ParameterField{Key: "image_fidelity", Label: "Image fidelity", Type: "number", Min: imageBound(0), Max: imageBound(1)}, core.ParameterField{Key: "human_fidelity", Label: "Facial fidelity", Type: "number", Min: imageBound(0), Max: imageBound(1)})
	}
	if op.id == MultiImage21 {
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"subject", "scene", "style"}, Default: "subject"}}
	}
	if op.id == ImageOutpainting {
		for _, key := range []string{"up_expansion_ratio", "down_expansion_ratio", "left_expansion_ratio", "right_expansion_ratio"} {
			m.Parameters = append(m.Parameters, core.ParameterField{Key: key, Label: strings.ReplaceAll(key, "_", " "), Type: "number", Required: true, Min: imageBound(0), Max: imageBound(2)})
		}
	}
	if op.noPrompt {
		m.NoWatermark = true
		m.MaxOutputs = 1
		m.Pricing.ReferenceDescription = "Published USD balance estimate per AI Multi-Shot call, including all returned views."
	}
	return m
}
func imageModels() []core.MediaModel {
	result := []core.MediaModel{}
	for _, op := range imageOperations() {
		result = append(result, imageModel(op))
	}
	return result
}
func imageInvalid(s string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, s) }

func (a *Adapter) validateImageRequest(r core.MediaRequest) error {
	op, ok := findImageOperation(r.Model)
	if !ok {
		return core.ErrModelUnavailable
	}
	m, o := imageModel(op), r.Options
	if utf8.RuneCountInString(imagePrompt(op, r.Prompt)) > 2500 || (!op.optionalPrompt && !op.noPrompt && strings.TrimSpace(r.Prompt) == "") || (op.noPrompt && r.Prompt != "") {
		return imageInvalid("unsupported Kling image prompt; maximum 2500 characters")
	}
	if o.OutputCount < 0 || o.OutputCount > m.MaxOutputs {
		return imageInvalid("unsupported Kling image output count")
	}
	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return imageInvalid("unsupported Kling image resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return imageInvalid("unsupported Kling image aspect ratio")
	}
	if op.id == ImageOmni3 && len(o.ImageURLs)+len(o.ResourceSnapshots) == 0 && o.Ratio == "auto" {
		return imageInvalid("Kling 3.0 Omni text-to-image requires an explicit aspect ratio")
	}
	if o.OverrideSoundID != "" || o.OverrideSound != nil || o.Duration != 0 || o.GenerateAudio != nil || o.CameraFixed != nil || o.Draft != nil || o.Seed != nil || o.ReturnLastFrame != nil || o.Quality != "" || o.Background != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || len(o.ReferenceVoices) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 || (op.noPrompt && o.Watermark != nil) {
		return imageInvalid("unsupported Kling image option")
	}
	if utf8.RuneCountInString(o.NegativePrompt) > 2500 || (o.NegativePrompt != "" && (!op.negative || len(o.ImageURLs) > 0)) {
		return imageInvalid("negative prompts are supported only for basic text-to-image")
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	for _, resource := range o.ResourceSnapshots {
		if !imageElementID.MatchString(resource.RemoteID) {
			return imageInvalid("Kling image element IDs must be positive decimal integers")
		}
		if _, err := strconv.ParseInt(resource.RemoteID, 10, 64); err != nil {
			return imageInvalid("Kling image element ID exceeds the native long range")
		}
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return err
	}
	for _, raw := range o.ImageURLs {
		if !publicURL(raw) {
			return imageInvalid("Kling image inputs require public HTTPS image URLs")
		}
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	if op.id == ImageGeneration21 {
		ref, _ := p["image_reference"].(string)
		if (ref != "" || p["image_fidelity"] != nil || p["human_fidelity"] != nil) && len(o.ImageURLs) == 0 {
			return imageInvalid("image reference controls require an image")
		}
		if p["human_fidelity"] != nil && ref != "subject" {
			return imageInvalid("human fidelity requires subject reference")
		}
	}
	if op.id == MultiImage21 {
		counts := map[string]int{}
		for i := range o.ImageURLs {
			counts[core.ReferenceRole(m, o, "image", i)]++
		}
		if counts["subject"] < 1 || counts["subject"] > 4 || counts["scene"] > 1 || counts["style"] > 1 {
			return imageInvalid("multi-image accepts 1–4 subjects, at most one scene and one style")
		}
	}
	if op.series {
		if p["result_type"] == "series" {
			if o.OutputCount != 0 {
				return imageInvalid("output_count is inapplicable to series; use series_amount")
			}
			if len(o.ImageURLs)+len(o.ResourceSnapshots) == 0 {
				return imageInvalid("series generation requires an image or element reference")
			}
		} else if p["series_amount"] != nil {
			return imageInvalid("series_amount requires result_type series")
		}
	}
	if op.id == ImageOutpainting {
		vertical := 1 + p["up_expansion_ratio"].(float64) + p["down_expansion_ratio"].(float64)
		horizontal := 1 + p["left_expansion_ratio"].(float64) + p["right_expansion_ratio"].(float64)
		if vertical*horizontal > 3+1e-12 {
			return imageInvalid("outpainting output area must not exceed three times the input area")
		}
	}
	return nil
}

// Translate common editor addresses only at the provider boundary. Native
// <<<image_1>>> mentions and unrelated @names remain unchanged.
var imageElementID = regexp.MustCompile(`^[1-9][0-9]*$`)
var imageEditorMention = regexp.MustCompile(`(?i)(^|[^\pL\pN_])@image_?([1-9][0-9]*)\b`)

func imagePrompt(op imageOperation, prompt string) string {
	if !op.omni {
		return prompt
	}
	return imageEditorMention.ReplaceAllStringFunc(prompt, func(match string) string {
		parts := imageEditorMention.FindStringSubmatch(match)
		return parts[1] + "<<<image_" + parts[2] + ">>>"
	})
}

type imageInput struct {
	Image string `json:"image"`
}
type subjectImageInput struct {
	Image string `json:"subject_image"`
}
type imageNativePayload struct {
	Elements       []imageElementInput `json:"element_list,omitempty"`
	ModelName      string              `json:"model_name,omitempty"`
	Prompt         string              `json:"prompt,omitempty"`
	NegativePrompt string              `json:"negative_prompt,omitempty"`
	Image          string              `json:"image,omitempty"`
	ImageList      []imageInput        `json:"image_list,omitempty"`
	SubjectImages  []subjectImageInput `json:"subject_image_list,omitempty"`
	SceneImage     string              `json:"scene_image,omitempty"`
	StyleImage     string              `json:"style_image,omitempty"`
	FrontalImage   string              `json:"element_frontal_image,omitempty"`
	ImageReference string              `json:"image_reference,omitempty"`
	ImageFidelity  *float64            `json:"image_fidelity,omitempty"`
	HumanFidelity  *float64            `json:"human_fidelity,omitempty"`
	Resolution     string              `json:"resolution,omitempty"`
	Ratio          string              `json:"aspect_ratio,omitempty"`
	Count          int                 `json:"n,omitempty"`
	ResultType     string              `json:"result_type,omitempty"`
	SeriesAmount   any                 `json:"series_amount,omitempty"`
	Up             *float64            `json:"up_expansion_ratio,omitempty"`
	Down           *float64            `json:"down_expansion_ratio,omitempty"`
	Left           *float64            `json:"left_expansion_ratio,omitempty"`
	Right          *float64            `json:"right_expansion_ratio,omitempty"`
	Watermark      *imageWatermark     `json:"watermark_info,omitempty"`
	ExternalTaskID string              `json:"external_task_id,omitempty"`
}
type imageElementInput struct {
	ID json.Number `json:"element_id"`
}

type imageWatermark struct {
	Enabled bool `json:"enabled"`
}

func imagePayload(op imageOperation, r core.MediaRequest) imageNativePayload {
	o := r.Options
	p, _ := core.NormalizeParameters(imageModel(op).Parameters, o.Parameters)
	body := imageNativePayload{ModelName: op.modelName, Prompt: imagePrompt(op, r.Prompt), NegativePrompt: o.NegativePrompt, Resolution: o.Resolution, Ratio: o.Ratio, Count: max(1, o.OutputCount), ExternalTaskID: r.TaskID}
	for _, resource := range o.ResourceSnapshots {
		body.Elements = append(body.Elements, imageElementInput{ID: json.Number(resource.RemoteID)})
	}
	if len(imageModel(op).Resolutions) > 0 && body.Resolution == "" {
		body.Resolution = "1k"
	}
	if len(imageModel(op).Ratios) > 0 && body.Ratio == "" {
		body.Ratio = "16:9"
		if op.omni && (op.id != ImageOmni3 || len(o.ImageURLs)+len(o.ResourceSnapshots) > 0) {
			body.Ratio = "auto"
		}
	}
	if o.Watermark != nil {
		body.Watermark = &imageWatermark{Enabled: *o.Watermark}
	}
	switch {
	case op.omni:
		for _, raw := range o.ImageURLs {
			body.ImageList = append(body.ImageList, imageInput{Image: raw})
		}
		body.ResultType = "single"
		if p["result_type"] == "series" {
			body.ResultType = "series"
			body.Count = 0
			amount, _ := p["series_amount"].(string)
			if amount == "" {
				amount = "4"
			}
			if amount == "auto" {
				body.SeriesAmount = "auto"
			} else {
				body.SeriesAmount, _ = strconv.Atoi(amount)
			}
		}
	case op.id == MultiImage21:
		for i, raw := range o.ImageURLs {
			switch core.ReferenceRole(imageModel(op), o, "image", i) {
			case "scene":
				body.SceneImage = raw
			case "style":
				body.StyleImage = raw
			default:
				body.SubjectImages = append(body.SubjectImages, subjectImageInput{Image: raw})
			}
		}
	case op.id == ImageMultiShot:
		body.FrontalImage = o.ImageURLs[0]
		body.Count = 0
	default:
		if len(o.ImageURLs) > 0 {
			body.Image = o.ImageURLs[0]
		}
	}
	if v, ok := p["image_reference"].(string); ok {
		body.ImageReference = v
	}
	if v, ok := p["image_fidelity"].(float64); ok {
		body.ImageFidelity = &v
	}
	if v, ok := p["human_fidelity"].(float64); ok {
		body.HumanFidelity = &v
	}
	if op.id == ImageOutpainting {
		up, down, left, right := p["up_expansion_ratio"].(float64), p["down_expansion_ratio"].(float64), p["left_expansion_ratio"].(float64), p["right_expansion_ratio"].(float64)
		body.Up, body.Down, body.Left, body.Right = &up, &down, &left, &right
	}
	return body
}
func (a *Adapter) generateImage(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validateImageRequest(r); err != nil {
		return core.ProviderTask{}, err
	}
	op, _ := findImageOperation(r.Model)
	return a.submitLegacy(ctx, op.id, op.path, imagePayload(op, r))
}
func (a *Adapter) quoteImage(r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.validateImageRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	op, _ := findImageOperation(r.Model)
	payload := imagePayload(op, r)
	quantity := float64(max(1, r.Options.OutputCount))
	rate := op.rate
	unit := "image"
	if op.id == ImageGeneration21 && len(r.Options.ImageURLs) > 0 {
		rate = 28000
	}
	if op.id == ImageOmni3 && payload.Resolution == "4k" {
		rate = 56000
	}
	if payload.ResultType == "series" {
		if payload.SeriesAmount == "auto" {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		quantity = float64(payload.SeriesAmount.(int))
	}
	if op.perCall {
		quantity = 1
		unit = "request"
	}
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: unit, Price: rate, Per: 1, Source: "https://kling.ai/document-api/pricing/base/image", Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price estimate; discounts, prepaid units and actual account charges remain unverified."}}, "", nil
}

// Raw scalar IDs accept strings and integer JSON tokens without float conversion.
type imageOpaqueID string

func (id *imageOpaqueID) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		*id = imageOpaqueID(s)
		return nil
	}
	s := string(raw)
	if s == "" {
		return errors.New("empty Kling ID")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return errors.New("invalid Kling numeric ID")
		}
	}
	*id = imageOpaqueID(s)
	return nil
}

type imageNativeArtifact struct {
	Index imageOpaqueID `json:"index"`
	URL   string        `json:"url"`
	URL1  string        `json:"url_1"`
	URL2  string        `json:"url_2"`
	URL3  string        `json:"url_3"`
}
type imageNativeTask struct {
	ID     imageOpaqueID `json:"task_id"`
	Status string        `json:"task_status"`
	Result struct {
		ResultType string                `json:"result_type"`
		Images     []imageNativeArtifact `json:"images"`
		Series     []imageNativeArtifact `json:"series_images"`
	} `json:"task_result"`
	Units   json.RawMessage `json:"final_unit_deduction"`
	Balance json.RawMessage `json:"final_balance_deduction"`
}

func (a *Adapter) getImageTask(ctx context.Context, operation, id string) (core.ProviderResult, error) {
	op, ok := findImageOperation(operation)
	if !ok || !validTaskID(id) {
		return core.ProviderResult{}, errors.New("invalid Kling image operation or task ID")
	}
	raw, err := a.do(ctx, http.MethodGet, op.path+"/"+id, nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	var task imageNativeTask
	if json.Unmarshal(raw, &task) != nil || string(task.ID) != id {
		return core.ProviderResult{}, errors.New("Kling image response is malformed or does not match the requested task")
	}
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling image task failed; check provider task details"}, nil
	case "succeed":
	default:
		return core.ProviderResult{}, errors.New("Kling image response has an unknown task status")
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded}
	seen := map[string]bool{}
	appendImages := func(images []imageNativeArtifact, group string) error {
		for i, item := range images {
			index := string(item.Index)
			if index == "" {
				index = strconv.Itoa(i)
			}
			urls := []string{item.URL}
			if op.perCall {
				urls = []string{item.URL1, item.URL2, item.URL3}
			}
			for j, rawURL := range urls {
				if !publicURL(rawURL) {
					return errors.New("Kling completed image task has an invalid image URL")
				}
				artifactID := group + ":" + index
				if op.perCall {
					artifactID += ":" + strconv.Itoa(j+1)
				}
				if seen[artifactID] {
					return errors.New("Kling completed image task has duplicate image indices")
				}
				seen[artifactID] = true
				artifactGroup := group
				if op.perCall {
					artifactGroup += ":" + index
				}
				result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: artifactID, MediaType: "image", URL: rawURL, GroupID: artifactGroup})
			}
		}
		return nil
	}
	if task.Result.ResultType != "" && task.Result.ResultType != "single" && task.Result.ResultType != "series" {
		return core.ProviderResult{}, errors.New("Kling image response has an unknown result type")
	}
	if err := appendImages(task.Result.Images, "images"); err != nil {
		return core.ProviderResult{}, err
	}
	if err := appendImages(task.Result.Series, "series"); err != nil {
		return core.ProviderResult{}, err
	}
	if len(result.Artifacts) == 0 || len(result.Artifacts) > 9 {
		return core.ProviderResult{}, errors.New("Kling completed image task has an invalid image count")
	}
	result.ResultURL = result.Artifacts[0].URL
	billing := map[string]json.RawMessage{}
	if len(task.Units) > 0 {
		billing["final_unit_deduction"] = task.Units
	}
	if len(task.Balance) > 0 {
		billing["final_balance_deduction"] = task.Balance
	}
	billingRaw, _ := json.Marshal(billing)
	metrics := []core.UsageMetric{{Name: "generated_images", Unit: "image", Quantity: float64(len(result.Artifacts))}}
	result.Usage = &core.ProviderUsage{Metrics: metrics, Raw: billingRaw, CostStatus: "metered"}
	return result, nil
}
