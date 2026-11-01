package minimax

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestFileLifecycleAndNativeReferenceMapping(t *testing.T) {
	ctx := owned("alice")
	calls := 0
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Path {
		case "/v1/files/upload":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Fatal(e)
			}
			if r.FormValue("purpose") != "video_generation_input" {
				t.Fatal("wrong purpose")
			}
			f, header, e := r.FormFile("file")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if header.Filename != "ref.png" || string(b) != "fixture" {
				t.Fatal(header, string(b))
			}
			return response(200, map[string]any{"file": map[string]any{"file_id": "9007199254740993", "purpose": "video_generation_input", "bytes": 7, "filename": "ref.png"}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/retrieve":
			return response(200, map[string]any{"file": map[string]any{"file_id": "9007199254740993", "purpose": "video_generation_input", "bytes": 7}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/list":
			return response(200, map[string]any{"files": []any{map[string]any{"file_id": "9007199254740993", "purpose": "video_generation_input", "bytes": 7}, map[string]any{"file_id": "999", "purpose": "video_generation_input", "bytes": 50}}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/retrieve_content":
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\nfixture"))}, nil
		case "/v2/video_generation":
			p := body(t, r)
			c := p["content"].([]any)
			if c[1].(map[string]any)["image_url"].(map[string]any)["url"] != "mm_file://9007199254740993" {
				t.Fatal(c)
			}
			return response(200, map[string]string{"task_id": "123"}), nil
		default:
			t.Fatal("unexpected route", r.URL)
		}
		return nil, nil
	})
	file, e := a.UploadFile(ctx, "ref.png", "image/png", strings.NewReader("fixture"))
	if e != nil || file.URI == "" || file.Handle == "" {
		t.Fatal(file, e)
	}
	if _, e = a.GetFile(ctx, file.Handle); e != nil {
		t.Fatal(e)
	}
	list, e := a.ListFiles(ctx, []string{file.Handle})
	if e != nil || len(list) != 1 || list[0].ID.String() != "9007199254740993" {
		t.Fatal(list, e)
	}
	data, e := a.DownloadFile(ctx, file.Handle, 15)
	if e != nil || string(data) != "\x89PNG\r\n\x1a\nfixture" {
		t.Fatal(string(data), e)
	}
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{file.URI}}}
	if _, e := a.Generate(ctx, r); e != nil {
		t.Fatal(e)
	}
	before := calls
	if _, e := a.GetFile(owned("bob"), file.Handle); e == nil {
		t.Fatal("foreign file read")
	}
	if _, e := a.DownloadFile(owned("bob"), file.Handle, 15); e == nil {
		t.Fatal("foreign download")
	}
	if _, e := a.Generate(owned("bob"), r); e == nil {
		t.Fatal("foreign file generation")
	}
	if e := a.DeleteFile(ctx, file.Handle); e == nil {
		t.Fatal("undocumented input deletion")
	}
	if calls != before {
		t.Fatal("rejected operation reached provider")
	}
}
func TestFileDeleteDocumentedOutputPurposeOnly(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/files/delete" {
			t.Fatal(r.URL)
		}
		p := body(t, r)
		if p["file_id"] != float64(123) || p["purpose"] != "video_generation" {
			t.Fatal(p)
		}
		return response(200, map[string]any{"file_id": 123, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	if e := a.DeleteFile(owned("alice"), a.seal(owned("alice"), handle{ID: "123", Kind: "file", Purpose: "video_generation"})); e != nil {
		t.Fatal(e)
	}
}
func TestFileInputFailuresAndDownloadLimits(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"private"}`))}, nil
	})
	for _, name := range []string{"../image.png", "image.gif", "video.png\r\n"} {
		if _, e := a.UploadFile(owned("alice"), name, "image/png", strings.NewReader("x")); e == nil {
			t.Fatal(name)
		}
	}
	token := a.seal(owned("alice"), handle{ID: "123", Kind: "file", Purpose: "video_generation_input"})
	if _, e := a.DownloadFile(owned("alice"), token, 100); e == nil {
		t.Fatal("JSON error body accepted as media")
	}
	if _, e := a.DownloadFile(owned("alice"), token, 0); e == nil {
		t.Fatal("unbounded download")
	}
	if _, e := a.GetFile(owned("alice"), "123"); e == nil {
		t.Fatal("raw file id accepted")
	}
	if _, e := a.resolveFileInput(owned("alice"), "asset://garbage", "image"); e == nil {
		t.Fatal("invalid asset accepted")
	}
	_ = json.Number("1")
}

func TestSignedFilesCannotChangeMediaKind(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("wrong file kind reached provider")
		return nil, nil
	})
	ctx := owned("alice")
	f := a.fileView(ctx, File{ID: json.Number("123"), Purpose: "video_generation_input", MediaKind: "audio"})
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{f.URI}}}
	if _, e := a.Generate(ctx, r); e == nil {
		t.Fatal("audio file accepted as image")
	}
}
func TestOwnedTaskToDeletableOutputFile(t *testing.T) {
	ctx := owned("alice")
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/query/video_generation":
			out := success("123")
			out["status"] = "Success"
			out["file_id"] = "456"
			return response(200, out), nil
		case "/v1/files/retrieve":
			return response(200, map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation"}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/delete":
			p := body(t, r)
			if p["file_id"] != float64(456) || p["purpose"] != "video_generation" {
				t.Fatal(p)
			}
			return response(200, map[string]any{"file_id": "456", "base_resp": map[string]int{"status_code": 0}}), nil
		default:
			t.Fatal(r.URL)
		}
		return nil, nil
	})
	task := a.seal(ctx, handle{ID: "123", Model: "MiniMax-Hailuo-02", Kind: "legacy"})
	f, e := a.GetTaskFile(ctx, task)
	if e != nil || f.Handle == "" || f.MediaKind != "video" {
		t.Fatal(f, e)
	}
	if e = a.DeleteFile(ctx, f.Handle); e != nil {
		t.Fatal(e)
	}
	if _, e = a.GetTaskFile(owned("bob"), task); e == nil {
		t.Fatal("foreign output file")
	}
}
func TestMislabelledBinaryDownload(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\nfixture"))}, nil
	})
	token := a.seal(owned("alice"), handle{ID: "123", Kind: "file", Purpose: "video_generation_input", MediaKind: "image"})
	if b, e := a.DownloadFile(owned("alice"), token, 100); e != nil || len(b) != 15 {
		t.Fatal(b, e)
	}
}

type countedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.reads += n
	return n, e
}
func (b *countedBody) Close() error { b.closed = true; return nil }
func TestProtectedArtifactFetchStreamsWithoutCredentialURL(t *testing.T) {
	data := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 4096)
	source := &countedBody{Reader: strings.NewReader(data)}
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery != "file_id=123" || strings.Contains(r.URL.String(), "fixture-key") {
			t.Fatal("credential in URL")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(data)), Body: source}, nil
	})
	token := a.seal(owned("alice"), handle{ID: "123", Kind: "file", Purpose: "video_generation_input", MediaKind: "image"})
	artifact, e := a.OpenArtifact(owned("alice"), token)
	reader, typ, size := artifact.Reader, artifact.ContentType, artifact.SizeBytes
	if e != nil || typ != "image/png" || size != int64(len(data)) {
		t.Fatal(typ, size, e)
	}
	if source.reads > 512 {
		t.Fatal("protected output buffered before archival", source.reads)
	}
	out, e := io.ReadAll(reader)
	reader.Close()
	if e != nil || string(out) != data || !source.closed {
		t.Fatal(len(out), e, source.closed)
	}
	source = &countedBody{Reader: strings.NewReader(data)}
	reader, _, _, e = a.openFile(owned("alice"), token, 100)
	if e == nil {
		_, e = io.ReadAll(reader)
		reader.Close()
	}
	if e == nil {
		t.Fatal("stream limit bypass")
	}
}

func TestDocumentedHEICHEIFAndMOVSignatures(t *testing.T) {
	for _, tc := range []struct{ brand, want string }{{"heic", "image/heic"}, {"mif1", "image/heif"}, {"qt  ", "video/quicktime"}, {"isom", "video/mp4"}} {
		data := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte(tc.brand)...)
		data = append(data, 0, 0, 0, 0)
		data = append(data, []byte(tc.brand)...)
		if got := sniffMedia(data); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestLegacyProtectedOutputUsesTypedStreamingReference(t *testing.T) {
	ctx := owned("alice")
	data := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte("isom0000isom")...)
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/query/video_generation":
			p := success("123")
			p["status"] = "Success"
			p["file_id"] = "456"
			return response(200, p), nil
		case "/v1/files/retrieve":
			return response(200, map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation", "bytes": len(data)}, "base_resp": map[string]int{"status_code": 0}}), nil
		case "/v1/files/retrieve_content":
			return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
		default:
			t.Fatal(r.URL)
		}
		return nil, nil
	})
	token := a.seal(ctx, handle{ID: "123", Kind: "legacy", Model: "MiniMax-Hailuo-02"})
	out, e := a.GetTask(ctx, token)
	if e != nil || len(out.Artifacts) != 1 || out.ResultURL != "" || out.Artifacts[0].URL != "" || out.Artifacts[0].SourceReference == "" {
		t.Fatal(out, e)
	}
	source, e := a.OpenArtifact(ctx, out.Artifacts[0].SourceReference)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Reader.Close()
	b, e := io.ReadAll(source.Reader)
	if e != nil || string(b) != string(data) || source.ContentType != "video/mp4" {
		t.Fatal(source, e)
	}
}
