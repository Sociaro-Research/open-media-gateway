package mock

import (
	_ "embed"
	"encoding/base64"
)

// Fixed audio sample for free mock speech and music results.
//
//go:embed assets/sample.mp3
var audioSample []byte

var embeddedAudioDataURL = "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(audioSample)
