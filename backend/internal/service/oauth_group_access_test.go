package service

import "testing"

func TestGroupVisibleForScope(t *testing.T) {
	cases := []struct {
		name                  string
		imageOnly             bool
		wantsImage, wantsChat bool
		want                  bool
	}{
		// image-only client: sees only image-only groups
		{"image client sees image-only group", true, true, false, true},
		{"image client hides text group", false, true, false, false},
		// chat-only client: excludes image-only groups, keeps text groups
		{"chat client hides image-only group", true, false, true, false},
		{"chat client sees text group", false, false, true, true},
		// the regression case: GPT-Pro is image_only=false even though
		// allow_image_generation=true — must stay visible to chat clients.
		{"chat client sees codex-gated text group", false, false, true, true},
		// both scopes: everything visible
		{"both scopes sees image-only", true, true, true, true},
		{"both scopes sees text", false, true, true, true},
		// neither scope: everything visible
		{"neither scope sees image-only", true, false, false, true},
		{"neither scope sees text", false, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupVisibleForScope(tc.imageOnly, tc.wantsImage, tc.wantsChat)
			if got != tc.want {
				t.Fatalf("groupVisibleForScope(%v,%v,%v) = %v, want %v",
					tc.imageOnly, tc.wantsImage, tc.wantsChat, got, tc.want)
			}
		})
	}
}
