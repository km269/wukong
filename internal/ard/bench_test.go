package ard

import (
	"testing"
	"time"
)

func BenchmarkCircuitBreaker_AllowRequest_Closed(b *testing.B) {
	cb := NewCircuitBreaker(5, 30*time.Second)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.AllowRequest()
	}
}

func BenchmarkCircuitBreaker_RecordFailure(b *testing.B) {
	cb := NewCircuitBreaker(5, 30*time.Second)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordFailure()
		cb.RecordSuccess()
	}
}

func BenchmarkCircuitBreaker_OpenRecovery(b *testing.B) {
	cb := NewCircuitBreaker(3, 1*time.Millisecond)
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	time.Sleep(2 * time.Millisecond)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.AllowRequest()
		cb.RecordSuccess()
	}
}

func BenchmarkResponseCache_Set(b *testing.B) {
	cache := NewResponseCache(5 * time.Minute)
	url := "https://example.com/api/data"
	body := []byte(`{"query":"test"}`)
	data := make([]byte, 1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(url, body, data)
	}
}

func BenchmarkResponseCache_Get_Hit(b *testing.B) {
	cache := NewResponseCache(5 * time.Minute)
	url := "https://example.com/api/data"
	body := []byte(`{"query":"test"}`)
	data := make([]byte, 1024)
	cache.Set(url, body, data)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(url, body)
	}
}

func BenchmarkResponseCache_Get_Miss(b *testing.B) {
	cache := NewResponseCache(5 * time.Minute)
	url := "https://example.com/api/data"
	body := []byte(`{"query":"miss"}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(url, body)
	}
}
