package router

import (
	"fmt"
	"testing"
)

func TestClassify_V1HasOneClassAndKeepsACallerClass(t *testing.T) {
	if got := Classify(Input{}); got != ClassDefault {
		t.Errorf("Classify = %q, want default", got)
	}
	if got := Classify(Input{Class: "chat"}); got != "chat" {
		t.Errorf("a caller's class is kept, got %q", got)
	}
	if got := Classify(Input{Class: "not valid!"}); got != ClassDefault {
		t.Errorf("an invalid class falls to default, got %q", got)
	}
}

// The seam K-140 uses: a replaced classifier decides the class, and a bad answer
// from it never reaches a rule or the ledger.
func TestClassify_SeamAndItsAnswerIsChecked(t *testing.T) {
	prev := ActiveClassifier
	t.Cleanup(func() { ActiveClassifier = prev })
	ActiveClassifier = func(Input) string { return "sensitive" }
	if got := Classify(Input{}); got != "sensitive" {
		t.Errorf("got %q", got)
	}
	ActiveClassifier = func(Input) string { return "bad class\n" }
	if got := Classify(Input{}); got != ClassDefault {
		t.Errorf("got %q", got)
	}
}

func TestSticky_FirstRoutingStandsPerConversationAndAgent(t *testing.T) {
	s := NewSticky()
	if _, ok := s.Get("", "a", "P", "h"); ok {
		t.Fatal("no conversation, no pin")
	}
	s.Put("c1", "a", Pin{Runtime: "codex", Model: "m1", Project: "P", PolicySHA: "h"}, false)
	s.Put("c1", "a", Pin{Runtime: "agy", Project: "P", PolicySHA: "h"}, false) // ignored: first wins
	s.Put("", "a", Pin{Runtime: "agy"}, true)                                  // ignored
	if p, ok := s.Get("c1", "a", "P", "h"); !ok || p.Runtime != "codex" || p.Model != "m1" {
		t.Fatalf("got %+v %v", p, ok)
	}
	if _, ok := s.Get("c1", "b", "P", "h"); ok {
		t.Error("another agent in the same conversation is routed on its own")
	}
	if _, ok := s.Get("c2", "a", "P", "h"); ok {
		t.Error("another conversation is routed on its own")
	}
}

func TestSticky_ExplicitReplaceRepins(t *testing.T) {
	s := NewSticky()
	s.Put("c", "a", Pin{Runtime: "codex", Project: "P", PolicySHA: "h"}, false)
	s.Put("c", "a", Pin{Runtime: "claude", Project: "P", PolicySHA: "h"}, true)
	if p, ok := s.Get("c", "a", "P", "h"); !ok || p.Runtime != "claude" {
		t.Fatalf("got %+v %v", p, ok)
	}
}

func TestSticky_PinIsScopedToProjectAndPolicy(t *testing.T) {
	s := NewSticky()
	s.Put("c", "a", Pin{Runtime: "codex", Project: "A", PolicySHA: "h1"}, false)
	if _, ok := s.Get("c", "a", "B", "h1"); ok {
		t.Error("another project must not see the pin")
	}
	if _, ok := s.Get("c", "a", "A", "h2"); ok {
		t.Error("another policy sha must not see the pin")
	}
	// A pin from another scope is replaced, not kept, by a non-replacing Put.
	s.Put("c", "a", Pin{Runtime: "agy", Project: "B", PolicySHA: "h1"}, false)
	if p, ok := s.Get("c", "a", "B", "h1"); !ok || p.Runtime != "agy" {
		t.Fatalf("got %+v %v", p, ok)
	}
	if _, ok := s.Get("c", "a", "A", "h1"); ok {
		t.Error("the old scope's pin is gone")
	}
}

func TestSticky_IsBounded(t *testing.T) {
	s := NewSticky()
	for i := 0; i < maxSticky*3; i++ {
		s.Put(fmt.Sprint("c", i), "a", Pin{Runtime: "codex"}, false)
	}
	if n := len(s.pins); n > maxSticky {
		t.Fatalf("table grew to %d, cap %d", n, maxSticky)
	}
	if _, ok := s.Get(fmt.Sprint("c", maxSticky*3-1), "a", "", ""); !ok {
		t.Error("the newest conversation must be kept")
	}
}
