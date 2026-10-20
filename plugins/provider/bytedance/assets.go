package bytedance

import _ "embed"

//go:embed icon.png
var embeddedIcon []byte

func Icon() []byte {
	return embeddedIcon
}
