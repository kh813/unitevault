package singleinstance

import (
	"fmt"
	"testing"
	"time"
)

// TestTryAcquire_SecondCallSeesFirstAsAlreadyRunning is a white-box test
// (package singleinstance, not singleinstance_test) so it can point
// lockFileName at a name unique to this test run - using the real default
// name would spuriously fail if an actual UniteVault instance happens to
// be running on the machine `go test` executes on.
func TestTryAcquire_SecondCallSeesFirstAsAlreadyRunning(t *testing.T) {
	original := lockFileName
	lockFileName = fmt.Sprintf("test-%d.lock", time.Now().UnixNano())
	t.Cleanup(func() { lockFileName = original })

	release, ok, err := TryAcquire()
	if err != nil {
		t.Fatalf("first TryAcquire failed: %v", err)
	}
	if !ok {
		t.Fatal("expected the first TryAcquire to succeed")
	}

	_, ok2, err2 := TryAcquire()
	if err2 != nil {
		t.Fatalf("second TryAcquire returned an unexpected error: %v", err2)
	}
	if ok2 {
		t.Fatal("expected the second TryAcquire to report the lock as already held")
	}

	release()

	release3, ok3, err3 := TryAcquire()
	if err3 != nil {
		t.Fatalf("TryAcquire after release failed: %v", err3)
	}
	if !ok3 {
		t.Fatal("expected TryAcquire to succeed again after the first lock was released")
	}
	release3()
}
