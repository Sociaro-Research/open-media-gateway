package bytedance

import (
	"strings"
	"testing"
)

const lifecycleNotice = "# Model deprecations\n# Fourth batch of model deprecations (November 11, 2026)\n| First Notification Date | Deprecation Date | Deactivation Date | Impact Description |\n| --- | --- | --- | --- |\n| September 8, 2026 | September 15, 2026 | November 11, 2026 | Cease service | \\\nThe seedance-1-5-pro-251215 model will be shut down after 17:00 on November 11, 2026 (UTC+8).\n| Model ID | Recommended Replacement | Automated Replacement |\n| --- | --- | --- |\n| seedance-1-5-pro-251215 | dreamina-seedance-2-0-mini-260615 | Deactivated |\n| seedance-future-version | dreamina-seedance-2-5-260628 | Deactivated |\n# Third batch of model deprecations (March 17, 2026)\n| First Notification Date | Deprecation Date | Deactivation Date | Impact Description |\n| --- | --- | --- | --- |\n| March 17, 2026 | March 23, 2026 | May 13, 2026 | Cease service |\n| Model ID | Recommended Replacement | Automated Replacement |\n| --- | --- | --- |\n| bytedance-seedance-1-0-lite-i2v-250428 | seedance-1-0-pro-fast-251015 | replacement |\n"

func TestBytePlusLifecycleUsesDeactivationAndExactOverride(t *testing.T) {
	entries, err := interpretLifecycle(lifecycleNotice)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Lifecycle.ShutdownAt != "2026-11-11T09:00:00Z" || entries[1].ID != "seedance-future-version" || entries[2].Lifecycle.ShutdownDate != "2026-05-13" || entries[2].ID != "seedance-1-0-lite-i2v-250428" {
		t.Fatal(entries)
	}
	for _, body := range []string{strings.Replace(lifecycleNotice, "17:00", "late afternoon", 1), strings.Replace(lifecycleNotice, "| November 11, 2026 |", "| November 12, 2026 |", 1), strings.Replace(lifecycleNotice, "May 13, 2026", "May 40, 2026", 1)} {
		if _, err := interpretLifecycle(body); err == nil {
			t.Fatal("accepted ambiguous lifecycle", body)
		}
	}
}

func TestFutureRegionalNoticeRemainsReviewEvidence(t *testing.T) {
	entries, err := interpretLifecycle(lifecycleNotice + "\n> Note: Region-specific deactivation\n> Seedance is deactivated for eu-west-1.\n> * `dreamina-seedance-future`\n# Background\n")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.ID == "dreamina-seedance-future" {
			found = true
			if e.Lifecycle != nil || e.Metadata == nil || len(e.Metadata.Facts) == 0 {
				t.Fatal("regional restriction became global shutdown", e)
			}
		}
	}
	if !found {
		t.Fatal("future regional notice disappeared")
	}
}
func TestFutureMediaExactTimeUsesTheReviewedNoticeShape(t *testing.T) {
	text := strings.ReplaceAll(lifecycleNotice, "seedance-1-5-pro-251215", "dreamina-seedance-new-version")
	entries, err := interpretLifecycle(text)
	if err != nil || entries[0].Lifecycle.ShutdownAt != "2026-11-11T09:00:00Z" {
		t.Fatal("future exact-ID evidence lost its time", entries, err)
	}
}
