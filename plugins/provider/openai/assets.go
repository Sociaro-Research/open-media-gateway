package openai

import _ "embed"

//go:embed icon.svg
var embeddedIcon []byte

func Icon() []byte { return embeddedIcon }
