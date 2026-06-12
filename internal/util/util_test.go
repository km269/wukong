package util

import (
	"testing"
)

func TestIntPtr(t *testing.T) {
	v := IntPtr(42)
	if v == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v != 42 {
		t.Errorf("expected 42, got %d", *v)
	}
}

func TestFloat64Ptr(t *testing.T) {
	v := Float64Ptr(0.7)
	if v == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v != 0.7 {
		t.Errorf("expected 0.7, got %f", *v)
	}
}

func TestBoolPtr(t *testing.T) {
	v := BoolPtr(true)
	if v == nil {
		t.Fatal("expected non-nil pointer")
	}
	if !*v {
		t.Error("expected true")
	}

	v2 := BoolPtr(false)
	if v2 == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v2 {
		t.Error("expected false")
	}
}

func TestStringPtr(t *testing.T) {
	v := StringPtr("hello")
	if v == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v != "hello" {
		t.Errorf("expected hello, got %q", *v)
	}

	v2 := StringPtr("")
	if v2 == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v2 != "" {
		t.Errorf("expected empty, got %q", *v2)
	}
}
