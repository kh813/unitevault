package syncdir_test

import (
	"testing"

	"github.com/kh813/unitevault/internal/syncdir"
)

// TestIsBookkeeping guards the real, previously-shipped bug this function
// fixes: a Google Drive remote populated before the .sync rename can still
// carry a top-level "_sync" folder, which a plain rclone copy/sync would
// otherwise keep re-downloading onto any device that later joins or
// re-syncs against that remote (reported on a real device: a freshly
// created local Vault picked up a stray "_sync" folder this way). Both the
// current and legacy bookkeeping directory names must be recognized.
func TestIsBookkeeping(t *testing.T) {
	cases := []struct {
		name     string
		slashRel string
		want     bool
	}{
		{name: "the current bookkeeping dir itself", slashRel: ".sync", want: true},
		{name: "a file inside the current bookkeeping dir", slashRel: ".sync/log-abc.jsonl", want: true},
		{name: "the legacy bookkeeping dir itself", slashRel: "_sync", want: true},
		{name: "a file inside the legacy bookkeeping dir", slashRel: "_sync/log-abc.jsonl", want: true},
		{name: "an ordinary note", slashRel: "Notes/todo.md", want: false},
		{name: "a folder that merely shares the current name as a prefix", slashRel: ".sync2/note.md", want: false},
		{name: "a folder that merely shares the legacy name as a prefix", slashRel: "_sync2/note.md", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := syncdir.IsBookkeeping(c.slashRel); got != c.want {
				t.Errorf("IsBookkeeping(%q) = %v, want %v", c.slashRel, got, c.want)
			}
		})
	}
}
