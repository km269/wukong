package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// stubCap is a minimal Capability for registry tests.
type stubCap struct {
	addr string
}

func (s *stubCap) Address() string { return s.addr }

func (s *stubCap) Descriptor() Descriptor {
	return Descriptor{
		Address: s.addr,
		Name:    s.addr,
		Source:  SourceBuiltin,
	}
}

func (s *stubCap) Invoke(
	_ context.Context, _ json.RawMessage,
) (json.RawMessage, error) {
	return json.RawMessage(`"ok"`), nil
}

func mustRegister(t *testing.T, r *Registry, addr string) {
	t.Helper()
	if err := r.Register(&stubCap{addr: addr}); err != nil {
		t.Fatalf("Register(%q): %v", addr, err)
	}
}

func TestRegisterAndResolve(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "tools.web.fetch")

	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
	got, ok := r.Resolve("tools.web.fetch")
	if !ok {
		t.Fatal("Resolve(tools.web.fetch) not found")
	}
	if got.Address() != "tools.web.fetch" {
		t.Fatalf("Address = %q, want %q",
			got.Address(), "tools.web.fetch")
	}
	if _, ok := r.Resolve("tools.web.missing"); ok {
		t.Fatal("Resolve(missing) should not be found")
	}
}

func TestRegisterValidation(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(nil); err == nil {
		t.Error("nil capability accepted")
	}
	if err := r.Register(&stubCap{addr: ""}); err == nil {
		t.Error("empty address accepted")
	}
	if err := r.Register(&stubCap{addr: "bare"}); err == nil {
		t.Error("namespace-less address accepted")
	}

	mustRegister(t, r, "tools.web.fetch")
	err := r.Register(&stubCap{addr: "tools.web.fetch"})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v, want ErrDuplicate", err)
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d after duplicate, want 1", got)
	}
}

func TestListSortedAndNamespaceFilter(t *testing.T) {
	r := NewRegistry()
	for _, a := range []string{
		"tools.web.fetch", "mcp.gh.issue",
		"tools.alpha", "tools.web.paginate",
	} {
		mustRegister(t, r, a)
	}

	var got []string
	for _, d := range r.List("") {
		got = append(got, d.Address)
	}
	want := []string{
		"mcp.gh.issue", "tools.alpha",
		"tools.web.fetch", "tools.web.paginate",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}

	if n := len(r.List("tools")); n != 3 {
		t.Fatalf("List(tools) len = %d, want 3", n)
	}
	if n := len(r.List("tools.web")); n != 2 {
		t.Fatalf("List(tools.web) len = %d, want 2", n)
	}
	if n := len(r.List("mcp")); n != 1 {
		t.Fatalf("List(mcp) len = %d, want 1", n)
	}
	if n := len(r.List("recipe")); n != 0 {
		t.Fatalf("List(recipe) len = %d, want 0", n)
	}
}

func TestUnregister(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "recipe.alpha")
	mustRegister(t, r, "recipe.beta")

	if !r.Unregister("recipe.alpha") {
		t.Error("Unregister(existing) = false")
	}
	if r.Unregister("recipe.alpha") {
		t.Error("Unregister(already removed) = true")
	}
	if r.Unregister("recipe.missing") {
		t.Error("Unregister(unknown) = true")
	}
	if _, ok := r.Resolve("recipe.alpha"); ok {
		t.Error("removed capability still resolvable")
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
}

func TestUnregisterNamespace(t *testing.T) {
	r := NewRegistry()
	for _, a := range []string{
		"recipe.a", "recipe.b.c", "tools.web.fetch", "recipe",
	} {
		// "recipe" alone lacks a namespace — skipped via bare stub
		// only if Register accepts it; it doesn't, so register the
		// rest directly.
		if a == "recipe" {
			continue
		}
		mustRegister(t, r, a)
	}

	removed := r.UnregisterNamespace("recipe")
	want := []string{"recipe.a", "recipe.b.c"}
	if fmt.Sprint(removed) != fmt.Sprint(want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1 (tools.web.fetch)", got)
	}
	if r.UnregisterNamespace("") != nil {
		t.Error("empty namespace should be a no-op returning nil")
	}
	if got := r.UnregisterNamespace("missing"); len(got) != 0 {
		t.Errorf("missing namespace removed = %v", got)
	}
}

func TestSearch(t *testing.T) {
	r := NewRegistry()
	for _, a := range []string{
		"tools.alpha", "tools.alpha_beta", "tools.beta", "mcp.alpha",
	} {
		mustRegister(t, r, a)
	}

	// Address prefix beats contains; ties break by address.
	if got := r.Search("tools.alpha", 0); len(got) != 2 ||
		got[0].Address != "tools.alpha" ||
		got[1].Address != "tools.alpha_beta" {
		t.Fatalf("Search(prefix) = %v", got)
	}

	// Contains matches across namespaces.
	got := r.Search("alpha", 0)
	if len(got) != 3 || got[0].Address != "mcp.alpha" {
		t.Fatalf("Search(contains) = %v", got)
	}

	// topK caps results.
	if got := r.Search("alpha", 2); len(got) != 2 {
		t.Fatalf("Search(topK=2) len = %d, want 2", len(got))
	}

	if got := r.Search("", 0); got != nil {
		t.Fatalf("Search(empty) = %v, want nil", got)
	}
	if got := r.Search("zzz-no-match", 0); len(got) != 0 {
		t.Fatalf("Search(no match) = %v, want empty", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &stubCap{addr: fmt.Sprintf("tools.pkg%d.tool", i)}
			_ = r.Register(c)
			_, _ = r.Resolve(c.addr)
			_ = r.List("tools")
			_ = r.Search("pkg", 5)
		}(i)
	}
	wg.Wait()
	if got := r.Len(); got != 32 {
		t.Fatalf("Len = %d, want 32", got)
	}
}
