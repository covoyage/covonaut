package component

import (
	"testing"
	"time"
)

func TestPasteBurstEnterAfterFastCharsIsNewline(t *testing.T) {
	var b pasteBurst
	t0 := time.Now()
	b.now = func() time.Time { return t0 }
	b.notePlain()
	b.now = func() time.Time { return t0.Add(time.Millisecond) }
	b.notePlain()
	b.now = func() time.Time { return t0.Add(2 * time.Millisecond) }
	b.notePlain()
	b.now = func() time.Time { return t0.Add(3 * time.Millisecond) }
	if !b.consumeEnterAsNewline() {
		t.Fatal("enter within burst interval should insert newline")
	}
}

func TestPasteBurstSlowTypingStillSubmits(t *testing.T) {
	var b pasteBurst
	t0 := time.Now()
	b.now = func() time.Time { return t0 }
	b.notePlain()
	b.now = func() time.Time { return t0.Add(50 * time.Millisecond) }
	if b.consumeEnterAsNewline() {
		t.Fatal("enter after normal typing delay should submit")
	}
}

func TestPasteBurstThreeFastCharsOpensWindow(t *testing.T) {
	var b pasteBurst
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		i := i
		b.now = func() time.Time { return t0.Add(time.Duration(i) * time.Millisecond) }
		b.notePlain()
	}
	b.now = func() time.Time { return t0.Add(20 * time.Millisecond) }
	if !b.consumeEnterAsNewline() {
		t.Fatal("enter inside suppress window should insert newline")
	}
	b.now = func() time.Time { return t0.Add(200 * time.Millisecond) }
	if b.consumeEnterAsNewline() {
		t.Fatal("enter after window expires should submit")
	}
}
