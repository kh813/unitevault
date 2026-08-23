package merge_test

import (
	"testing"

	"github.com/kh813/unitevault/internal/merge"
)

func TestMergeContents_CleanMerge(t *testing.T) {
	base := "Line 1\nLine 2\nLine 3\n"
	versionA := "Line 1 (edited by A)\nLine 2\nLine 3\n"
	versionB := "Line 1\nLine 2\nLine 3 (edited by B)\n"

	res, err := merge.MergeContents(versionA, base, versionB)
	if err != nil {
		t.Fatalf("MergeContents returned error: %v", err)
	}

	if res.HasConflict {
		t.Fatalf("expected no conflict in clean merge")
	}

	expected := "Line 1 (edited by A)\nLine 2\nLine 3 (edited by B)\n"
	if res.MergedContent != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, res.MergedContent)
	}
}

func TestMergeContents_Conflict(t *testing.T) {
	base := "Line 1\nLine 2\nLine 3\n"
	versionA := "Line 1 (edited by A)\nLine 2\nLine 3\n"
	versionB := "Line 1 (edited by B)\nLine 2\nLine 3\n"

	res, err := merge.MergeContents(versionA, base, versionB)
	if err != nil {
		t.Fatalf("MergeContents returned error: %v", err)
	}

	if !res.HasConflict {
		t.Fatalf("expected conflict marker flag to be true")
	}

	if !merge.HasConflictMarkers(res.MergedContent) {
		t.Fatalf("expected conflict markers (<<<<<<<) in merged content")
	}
}

func TestNWayMerge_ThreeDevices(t *testing.T) {
	base := "Title\n\nSection A\n\nSection B\n\nSection C\n"
	devA := merge.DeviceVersion{DeviceID: "devA", Content: "Title\n\nSection A (updated A)\n\nSection B\n\nSection C\n"}
	devB := merge.DeviceVersion{DeviceID: "devB", Content: "Title\n\nSection A\n\nSection B (updated B)\n\nSection C\n"}
	devC := merge.DeviceVersion{DeviceID: "devC", Content: "Title\n\nSection A\n\nSection B\n\nSection C (updated C)\n"}

	versions := []merge.DeviceVersion{devA, devB, devC}
	res, err := merge.NWayMerge(base, versions)
	if err != nil {
		t.Fatalf("NWayMerge returned error: %v", err)
	}

	if res.HasConflict {
		t.Fatalf("expected no conflict in 3-way clean merge")
	}

	expected := "Title\n\nSection A (updated A)\n\nSection B (updated B)\n\nSection C (updated C)\n"
	if res.MergedContent != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, res.MergedContent)
	}
}
