package router

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestCooldown_ThreeFailuresCoolForSixtySeconds(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	c := NewCooldown(clk.now)
	c.Failure("codex")
	c.Failure("codex")
	if cool, _ := c.Cooling("codex"); cool {
		t.Fatal("two failures must not cool")
	}
	c.Failure("codex")
	cool, left := c.Cooling("codex")
	if !cool || left != 60*time.Second {
		t.Fatalf("third failure: cooling=%v left=%v, want true 60s", cool, left)
	}
	if cool, _ := c.Cooling("claude"); cool {
		t.Error("another runtime is not affected")
	}
	clk.advance(59 * time.Second)
	if cool, _ := c.Cooling("codex"); !cool {
		t.Error("still cooling at 59s")
	}
	clk.advance(time.Second)
	if cool, _ := c.Cooling("codex"); cool {
		t.Error("cooldown must end at 60s")
	}
	// The count starts over: two more failures do not cool.
	c.Failure("codex")
	c.Failure("codex")
	if cool, _ := c.Cooling("codex"); cool {
		t.Error("the count must restart after a cooldown")
	}
}

func TestCooldown_SuccessResetsTheCount(t *testing.T) {
	c := NewCooldown((&fakeClock{t: time.Unix(1, 0)}).now)
	c.Failure("agy")
	c.Failure("agy")
	c.Success("agy")
	c.Failure("agy")
	if cool, _ := c.Cooling("agy"); cool {
		t.Fatal("a success in between means the failures were not three in a row")
	}
}

func TestCooldown_FailuresWhileCoolingDoNotExtendIt(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1, 0)}
	c := NewCooldown(clk.now)
	for i := 0; i < 3; i++ {
		c.Failure("codex")
	}
	clk.advance(30 * time.Second)
	for i := 0; i < 5; i++ {
		c.Failure("codex")
	}
	if _, left := c.Cooling("codex"); left != 30*time.Second {
		t.Fatalf("left = %v, want 30s", left)
	}
}
