package httpclient

import (
	"sync"
	"testing"
	"time"
)

func BenchmarkDNSCacheLookup_Hit(b *testing.B) {
	cache := NewDNSCache(5 * time.Minute)
	cache.Lookup("example.com")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cache.Lookup("example.com")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDNSCacheLookup_MissThenHit(b *testing.B) {
	cache := NewDNSCache(5 * time.Minute)
	hosts := make([]string, 100)
	for i := range hosts {
		hosts[i] = "host" + string(rune('a'+i%26)) + ".example.com"
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		host := hosts[i%len(hosts)]
		cache.Lookup(host)
	}
}

func BenchmarkDNSCacheLookup_Parallel(b *testing.B) {
	cache := NewDNSCache(5 * time.Minute)
	cache.Lookup("example.com")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Lookup("example.com")
		}
	})
}

func BenchmarkRateLimiter_Allow(b *testing.B) {
	limiter := NewRateLimiter(1000, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow("example.com")
	}
}

func BenchmarkRateLimiter_AllowParallel(b *testing.B) {
	limiter := NewRateLimiter(10000, 1000)

	var wg sync.WaitGroup
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			limiter.Allow("example.com")
		}
	})
	wg.Wait()
}

func BenchmarkRateLimiter_BlockHost(b *testing.B) {
	limiter := NewRateLimiter(1000, 100)
	for i := 0; i < b.N; i++ {
		limiter.BlockHost("slow.example.com", 100*time.Millisecond)
	}
}
