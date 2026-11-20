package bytedance

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// GW-400: the 2.5 series takes reference videos up to 4096×2160 pixels, 2.0 keeps
// 3326×2494. Descriptors, the saved official line and validation must agree.
func TestSeedanceReferenceVideoPixelBoundsFollowTheTaskAPI(t *testing.T) {
	body, err := os.ReadFile("testdata/video-api/seedance-2-5-series-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	published := map[string][2]int{}
	bounds := regexp.MustCompile(`Dreamina Seedance (2\.[05]) series:\*\* .\[\d+ × \d+ = (\d+), \d+ × \d+ = (\d+)\].`)
	for _, m := range bounds.FindAllStringSubmatch(string(body), -1) {
		low, _ := strconv.Atoi(m[2])
		high, _ := strconv.Atoi(m[3])
		published[m[1]] = [2]int{low, high}
	}
	if published["2.5"] != [2]int{407696, 8847360} || published["2.0"] != [2]int{407696, 8295044} {
		t.Fatal("saved task API pixel bounds", published)
	}
	ok := func(id string, w, h int) error {
		streams := 1
		return referencevalidation.ValidateFiles(describeModel(id), []referencevalidation.File{{Type: "video", Name: "Video 1", Info: mediafile.Info{ContentType: "video/mp4", Codec: "avc1", Size: 1000, Width: w, Height: h, Duration: 5, FPS: 30, VideoStreams: &streams}}}, false)
	}
	for _, id := range []string{base25ID, flashID, "dreamina-seedance-2-0-260128", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615"} {
		series := "2.0"
		if strings.HasPrefix(id, "dreamina-seedance-2-5-") {
			series = "2.5"
		}
		limits := describeModel(id).ReferenceMediaLimits.Video
		if [2]int{int(limits.MinPixels), int(limits.MaxPixels)} != published[series] {
			t.Fatal(id, "descriptor pixel bounds differ from the task API", limits.MinPixels, limits.MaxPixels)
		}
		if err := ok(id, 614, 664); err != nil {
			t.Fatal(id, "lower bound rejected", err)
		}
		if err := ok(id, 614, 663); err == nil || !strings.Contains(err.Error(), "pixels") {
			t.Fatal(id, "below the lower bound accepted", err)
		}
		if err := ok(id, 4096, 2160); (series == "2.5") != (err == nil) {
			t.Fatal(id, "4096×2160 reference video", err)
		}
		if err := ok(id, 4097, 2160); err == nil || !strings.Contains(err.Error(), "pixels") {
			t.Fatal(id, "above the 2.5 maximum accepted", err)
		}
	}
}
