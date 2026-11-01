package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func acceptedReceiptFixture() (Task, ProviderTask) {
	t := Task{ID: "5d98d8c2-62ed-4604-8a4d-bb703404194e", OwnerID: "receipt-owner", ProviderOwnerID: "provider-owner", StorageOwnerID: "storage-owner", Provider: "provider--connection", Model: "reviewed-operation", ProviderIdentity: "actual-connection-revision", MediaType: "image"}
	p := ProviderTask{Artifacts: []ProviderArtifact{{ID: "upstream-one", MediaType: "image", Format: "png", URL: "https://cdn.fixture/one.png"}, {ID: "upstream-two", MediaType: "image", Format: "png", URL: "https://cdn.fixture/two.png"}}, ResultURL: "https://cdn.fixture/one.png", Usage: &ProviderUsage{Metrics: []UsageMetric{{Name: "image_count", Unit: "image", Quantity: 2}}, Raw: json.RawMessage(`{"accepted":true}`)}}
	return t, p
}
func TestAcceptedReceiptRoundTripBindingAndIsolation(t *testing.T) {
	task, p := acceptedReceiptFixture()
	raw, digest, e := EncodeAcceptedReceipt(task, p)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		t.Fatal("not digest of exact bytes")
	}
	got, e := DecodeAcceptedReceipt(task, raw, digest)
	if e != nil || !reflect.DeepEqual(*got, p) {
		t.Fatal(got, e)
	}
	p.Artifacts[0].URL = "https://changed.fixture"
	p.Usage.Metrics[0].Quantity = 99
	if got.Artifacts[0].URL != "https://cdn.fixture/one.png" || got.Usage.Metrics[0].Quantity != 2 {
		t.Fatal("codec shared mutable result")
	}
	for _, field := range []string{"ID", "OwnerID", "ProviderOwnerID", "StorageOwnerID", "Provider", "Model", "ProviderIdentity", "MediaType", "ActorID"} {
		t.Run(field, func(t *testing.T) {
			changed := task
			reflect.ValueOf(&changed).Elem().FieldByName(field).SetString("foreign")
			if _, e := DecodeAcceptedReceipt(changed, raw, digest); !errors.Is(e, ErrAcceptedReceiptInvalid) {
				t.Fatal("foreign task decoded", e)
			}
		})
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[len(corrupt)-2] ^= 1
	if _, e := DecodeAcceptedReceipt(task, corrupt, digest); !errors.Is(e, ErrAcceptedReceiptInvalid) {
		t.Fatal("tamper decoded", e)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	for _, edit := range []func(map[string]any){func(m map[string]any) { m["version"] = 2 }, func(m map[string]any) { m["unreviewed"] = true }} {
		edit(envelope)
		bad, _ := json.Marshal(envelope)
		d := sha256.Sum256(bad)
		if _, e := DecodeAcceptedReceipt(task, bad, hex.EncodeToString(d[:])); !errors.Is(e, ErrAcceptedReceiptInvalid) {
			t.Fatal("unknown schema decoded", e)
		}
	}
	public, _ := json.Marshal(GenerationView(task))
	if bytes.Contains(public, []byte("cdn.fixture")) || bytes.Contains(public, []byte("receipt")) {
		t.Fatal("receipt entered public task view")
	}
}
func TestAcceptedReceiptRejectsUnboundedAndMalformedNormalizedResults(t *testing.T) {
	edits := map[string]func(*ProviderTask){
		"async without immediate output": func(p *ProviderTask) { *p = ProviderTask{ID: "real-native-id"} },
		"empty accepted result":          func(p *ProviderTask) { *p = ProviderTask{} },
		"handle carries payload":         func(p *ProviderTask) { p.ID = "data:image/png;base64,AAAA" },
		"too many artifacts": func(p *ProviderTask) {
			for len(p.Artifacts) < 65 {
				p.Artifacts = append(p.Artifacts, p.Artifacts[0])
			}
		},
		"too many resources": func(p *ProviderTask) { p.Resources = make([]ProviderResource, 65) },
		"multiple sources":   func(p *ProviderTask) { p.Artifacts[0].SourceReference = "private-source" },
		"missing source":     func(p *ProviderTask) { p.Artifacts[0].URL = ""; p.ResultURL = "" },
		"unknown kind":       func(p *ProviderTask) { p.Artifacts[0].MediaType = "unreviewed" },
		"binary raw URL":     func(p *ProviderTask) { p.Artifacts[0].MediaType = "binary" },
		"text in media":      func(p *ProviderTask) { p.Artifacts[0].URL = ""; p.ResultURL = ""; p.Artifacts[0].Text = "bytes" },
		"invalid JSON": func(p *ProviderTask) {
			p.Artifacts[0].URL = ""
			p.ResultURL = ""
			p.Artifacts[0].MediaType = "json"
			p.Artifacts[0].Text = "{"
		},
		"oversized text": func(p *ProviderTask) {
			p.Artifacts[0].URL = ""
			p.ResultURL = ""
			p.Artifacts[0].MediaType = "text"
			p.Artifacts[0].Text = strings.Repeat("a", MaxAcceptedReceiptMetadataBytes+1)
		},
		"escaped JSON exceeds final bound": func(p *ProviderTask) {
			p.Artifacts[0].URL = ""
			p.ResultURL = ""
			p.Artifacts[0].MediaType = "text"
			p.Artifacts[0].Text = strings.Repeat("\x00", MaxAcceptedReceiptMetadataBytes/3)
		},
		"wrong primary":          func(p *ProviderTask) { p.ResultURL = "https://cdn.fixture/unrelated.png" },
		"invalid URL":            func(p *ProviderTask) { p.ResultURL = ""; p.Artifacts[0].URL = "file:///private" },
		"credential URL":         func(p *ProviderTask) { p.ResultURL = ""; p.Artifacts[0].URL = "https://secret:token@cdn.fixture/image" },
		"nonfinite duration":     func(p *ProviderTask) { p.Artifacts[0].Duration = math.Inf(1) },
		"negative metric":        func(p *ProviderTask) { p.Usage.Metrics[0].Quantity = -1 },
		"nonfinite usage":        func(p *ProviderTask) { p.Usage.Metrics[0].Quantity = math.NaN() },
		"invalid usage raw":      func(p *ProviderTask) { p.Usage.Raw = json.RawMessage(`{`) },
		"negative money":         func(p *ProviderTask) { v := -1.0; p.Usage.EstimatedCost = &v },
		"invalid UTF8":           func(p *ProviderTask) { p.Artifacts[0].ID = string([]byte{255}) },
		"malformed inline media": func(p *ProviderTask) { p.ResultURL = ""; p.Artifacts[0].URL = "data:image/png;base64,!" },
		"recursive resource": func(p *ProviderTask) {
			r := &ProviderResource{RemoteID: "remote", Kind: "voice", Status: ProviderSucceeded}
			r.BoundVoice = r
			p.Resources = []ProviderResource{*r}
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			task, p := acceptedReceiptFixture()
			edit(&p)
			if _, _, e := EncodeAcceptedReceipt(task, p); !errors.Is(e, ErrAcceptedReceiptInvalid) {
				t.Fatal("malformed accepted", e)
			}
		})
	}
}
func TestAcceptedReceiptPreservesTypedVariantsAndResources(t *testing.T) {
	task, p := acceptedReceiptFixture()
	duration := 4.0
	p.ID = "actual-native-operation"
	p.ResultURL = ""
	p.LastFrameURL = "https://cdn.fixture/last.png"
	p.Artifacts = []ProviderArtifact{{ID: "json", MediaType: "json", Format: "json", Text: `{"action":"preview"}`}, {ID: "track", MediaType: "binary", Format: "bin", SourceReference: "authenticated-scope-source"}, {ID: "inline", MediaType: "image", Format: "png", URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("synthetic"))}}
	p.Resources = []ProviderResource{{RemoteID: "private-session-id", Kind: "editing_session", Subtype: "selection", Status: ProviderSucceeded, Sources: []ResourceSourceInput{{ReferenceID: "authorized-local", URL: "https://gateway.fixture/references/signed", MediaType: "video", Duration: &duration, FPS: 30, FrameCount: 120}}, Session: &MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FPS: 30, FrameCount: 120, SelectionRevision: 1, HasSelection: true, Selections: []FrameSelection{{FrameIndex: 0, Points: []SelectionPoint{{X: .7738498789346246, Y: .297142857142857}}}}}}}
	raw, d, e := EncodeAcceptedReceipt(task, p)
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeAcceptedReceipt(task, raw, d)
	if e != nil || !reflect.DeepEqual(*got, p) {
		t.Fatal("typed outcomes lost", got, e)
	}
	p = ProviderTask{Resources: p.Resources}
	if _, _, e = EncodeAcceptedReceipt(task, p); e != nil {
		t.Fatal("resource-only immediate outcome rejected", e)
	}
}

func TestAcceptedReceiptLegacyEmptyRevisionIsBoundExactly(t *testing.T) {
	task, p := acceptedReceiptFixture()
	task.ProviderIdentity = ""
	raw, d, e := EncodeAcceptedReceipt(task, p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeAcceptedReceipt(task, raw, d); e != nil {
		t.Fatal(e)
	}
	task.ProviderIdentity = "new-connection"
	if _, e = DecodeAcceptedReceipt(task, raw, d); !errors.Is(e, ErrAcceptedReceiptInvalid) {
		t.Fatal("legacy receipt rebound to connection", e)
	}
}

func TestAcceptedReceiptLargeInlineImageUsesSinglePrivatePayload(t *testing.T) {
	var pngBytes bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	im := image.NewNRGBA(image.Rect(0, 0, 1024, 768))
	if e := encoder.Encode(&pngBytes, im); e != nil {
		t.Fatal(e)
	}
	if _, e := png.Decode(bytes.NewReader(pngBytes.Bytes())); e != nil {
		t.Fatal("invalid synthetic image", e)
	}
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	if len(imageURL) <= 2<<20 {
		t.Fatal("fixture does not exercise prior cap", len(imageURL))
	}
	task, p := acceptedReceiptFixture()
	p.ResultURL = imageURL
	p.Artifacts[0].URL = imageURL
	raw, d, e := EncodeAcceptedReceipt(task, p)
	if e != nil {
		t.Fatal("existing supported inline response rejected", e)
	}
	if len(raw) > len(imageURL)+4096 || bytes.Count(raw, []byte("data:image/png;base64,")) != 1 {
		t.Fatal("primary inline payload duplicated", len(raw), len(imageURL))
	}
	got, e := DecodeAcceptedReceipt(task, raw, d)
	if e != nil || got.ResultURL != imageURL || got.Artifacts[0].URL != imageURL || got.ID != p.ID {
		t.Fatal("primary alias did not restore exact result", e)
	}
	p.ID = strings.Repeat("x", 4097)
	if _, _, e = EncodeAcceptedReceipt(task, p); !errors.Is(e, ErrAcceptedReceiptInvalid) {
		t.Fatal("payload-sized operation ID allowed", e)
	}
}
func TestAcceptedReceiptDecodeRejectsOneByteAbovePrivateEnvelopeBound(t *testing.T) {
	task, _ := acceptedReceiptFixture()
	// The size check must run before hashing/decoding the oversized buffer.
	raw := make([]byte, MaxAcceptedReceiptBytes+1)
	if _, e := DecodeAcceptedReceipt(task, raw, strings.Repeat("0", 64)); !errors.Is(e, ErrAcceptedReceiptInvalid) {
		t.Fatal("oversized receipt decoded", e)
	}
}
