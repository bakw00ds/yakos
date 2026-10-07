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
	if _, ok := s.Get("", "a"); ok {
		t.Fatal("no conversation, no pin")
	}
	s.Put("c1", "a", Pin{Runtime: "codex", Model: "m1"})
	s.Put("c1", "a", Pin{Runtime: "agy"}) // ignored
	s.Put("", "a", Pin{Runtime: "agy"})   // ignored
	if p, ok := s.Get("c1", "a"); !ok || p.Runtime != "codex" || p.Model != "m1" {
		t.Fatalf("got %+v %v", p, ok)
	}
	if _, ok := s.Get("c1", "b"); ok {
		t.Error("another agent in the same conversation is routed on its own")
	}
	if _, ok := s.Get("c2", "a"); ok {
		t.Error("another conversation is routed on its own")
	}
}

func TestSticky_IsBounded(t *testing.T) {
	s := NewSticky()
	for i := 0; i < maxSticky*3; i++ {
		s.Put(fmt.Sprint("c", i), "a", Pin{Runtime: "codex"})
	}
	if n := len(s.pins); n > maxSticky {
		t.Fatalf("table grew to %d, cap %d", n, maxSticky)
	}
	if _, ok := s.Get(fmt.Sprint("c", maxSticky*3-1), "a"); !ok {
		t.Error("the newest conversation must be kept")
	}
}
