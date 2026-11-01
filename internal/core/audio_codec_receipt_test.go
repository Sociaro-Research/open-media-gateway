package core

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAudioCodecReceiptBoundsBeforeSerializationAndPreservesUnknown(t *testing.T) {
	task, p := acceptedReceiptFixture()
	streams := 2
	p.Resources = []ProviderResource{{Kind: "file_resource", Subtype: "video", RemoteID: "codec-source", Status: ProviderSucceeded, Sources: []ResourceSourceInput{{MediaType: "video", AudioStreams: &streams, AudioCodecs: []string{"aac", "mp3"}}}}}
	raw, digest, err := EncodeAcceptedReceipt(task, p)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := DecodeAcceptedReceipt(task, raw, digest)
	if err != nil || !reflect.DeepEqual(reopened.Resources[0].Sources[0].AudioCodecs, []string{"aac", "mp3"}) {
		t.Fatal(reopened, err)
	}
	p.Resources[0].Sources[0].AudioCodecs[0] = "opus"
	if reopened.Resources[0].Sources[0].AudioCodecs[0] != "aac" {
		t.Fatal("receipt shares mutable codec slice")
	}
	for _, bad := range [][]string{make([]string, 257), {strings.Repeat("a", 129)}} {
		p.Resources[0].Sources[0].AudioCodecs = bad
		if !errors.Is(ValidateAcceptedProviderTask(p), ErrAcceptedReceiptInvalid) {
			t.Fatal("oversized codec list/entry passed pre-marshal validation")
		}
		if _, _, err = EncodeAcceptedReceipt(task, p); !errors.Is(err, ErrAcceptedReceiptInvalid) {
			t.Fatal("oversized metadata serialized", err)
		}
	}
	// Receipt validation bounds metadata; it must not impose a model's audio
	// allowlist or upgrade unknown streams to silence.
	for _, count := range []*int{nil, codecCount(0), codecCount(1)} {
		p.Resources[0].Sources[0].AudioStreams = count
		p.Resources[0].Sources[0].AudioCodecs = nil
		raw, digest, err = EncodeAcceptedReceipt(task, p)
		if err != nil {
			t.Fatal("unknown codec metadata became a universal model rejection", err)
		}
		got, err := DecodeAcceptedReceipt(task, raw, digest)
		if err != nil || !reflect.DeepEqual(got.Resources[0].Sources[0].AudioStreams, count) {
			t.Fatal("unknown/silence count changed", got, err)
		}
	}
	p.Resources[0].Sources[0].AudioCodecs = []string{strings.Repeat("a", 128)}
	if err = ValidateAcceptedProviderTask(p); err != nil {
		t.Fatal("exact metadata string boundary rejected", err)
	}
}
