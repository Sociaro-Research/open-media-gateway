package templates

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// UIReferenceSyntax versions new authoring without reinterpreting saved recipes.
const UIReferenceSyntax = "ui-reference-v1"

var commonReference = regexp.MustCompile(`(?i)([<\[{])(Image|Picture|Video|Audio)[ _]?([+-]?[0-9]+(?:\.[0-9]+)?)([>\]}])|@(Image|Picture|Video|Audio)_?([+-]?[0-9]+(?:\.[0-9]+)?)`)

// inferCommon builds bindings only. The original prompt is never substituted or
// assigned to a request; native provider mention recommendations are independent.
func inferCommon(prompt string, types []string, mode core.ReferenceMode) ([]Slot, error) {
	var addresses strings.Builder
	for _, m := range commonReference.FindAllStringSubmatchIndex(prompt, -1) {
		before, _ := utf8.DecodeLastRuneInString(prompt[:m[0]])
		after, _ := utf8.DecodeRuneInString(prompt[m[1]:])
		if m[0] > 0 && !(unicode.IsSpace(before) || strings.ContainsRune("('\"“«:;,.!?>]}", before)) || m[1] < len(prompt) && (unicode.IsLetter(after) || unicode.IsNumber(after) || strings.ContainsRune("_>]}@", after)) {
			continue
		}
		group := func(i int) string {
			if m[2*i] < 0 {
				return ""
			}
			return prompt[m[2*i]:m[2*i+1]]
		}
		word, number := group(2), group(3)
		if open := group(1); open != "" {
			if map[string]string{"<": ">", "[": "]", "{": "}"}[open] != group(4) {
				continue
			}
		} else {
			word, number = group(5), group(6)
		}
		if strings.EqualFold(word, "Picture") {
			word = "Image"
		}
		n, err := strconv.Atoi(number)
		if err != nil || !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(number) || n > 64 {
			return nil, fmt.Errorf("%w: invalid reference index", ErrInvalid)
		}
		addresses.WriteString("@" + word + number + " ")
	}
	return Infer(addresses.String(), "numbered", types, mode)
}
func referenceTypes(model core.MediaModel, mode core.ReferenceMode) []string {
	var types []string
	for _, item := range []struct {
		kind string
		max  int
	}{{"image", mode.MaxImages}, {"video", mode.MaxVideos}, {"audio", mode.MaxAudios}} {
		if item.max > 0 {
			types = append(types, item.kind)
		}
	}
	return types
}
