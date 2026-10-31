package isureal

import (
	"fmt"
	"testing"
)

func TestNormalizeSearchTextUnicodeGoldens(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"Straße", "strasse"},
		{"ẞ", "ss"},
		{"Cafe\u0301", "café"},
		{"café", "café"},
		{"ΟΣ", "οσ"},
		{"ος", "οσ"},
		{"I", "i"},
		{"İ", "i\u0307"},
		{"ı", "ı"},
	} {
		if got := normalizeSearchText(test.input); got != test.want {
			t.Errorf("normalizeSearchText(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if got := normalizeSearchText("ß"); got != normalizeSearchText("ss") {
		t.Errorf("fold-equivalent spellings differ: %q vs %q", got, normalizeSearchText("ss"))
	}
}

func TestNormalizeSearchTextUsesRawInputKey(t *testing.T) {
	resetNormalizeSearchTextCache()
	if got := normalizeSearchText("Display Name v1"); got != "display name v1" {
		t.Fatalf("first value = %q", got)
	}
	if got := normalizeSearchText("Display Name v2"); got != "display name v2" {
		t.Fatalf("changed raw value = %q", got)
	}
	if got := normalizeSearchText("Display Name v1"); got != "display name v1" {
		t.Fatalf("returning to old raw value = %q", got)
	}
	normalizeSearchTextCache.RLock()
	defer normalizeSearchTextCache.RUnlock()
	if got := normalizeSearchTextCache.values["Display Name v2"]; got != "display name v2" {
		t.Errorf("cache does not contain the raw-keyed changed value: %q", got)
	}
}

func TestNormalizeSearchTextCacheBound(t *testing.T) {
	resetNormalizeSearchTextCache()
	for i := 0; i <= normalizeSearchTextCacheMaxEntries; i++ {
		input := fmt.Sprintf("entry-%d", i)
		if got, want := normalizeSearchText(input), normalizeSearchTextUncached(input); got != want {
			t.Fatalf("entry %d = %q, want %q", i, got, want)
		}
	}
	normalizeSearchTextCache.RLock()
	entries := len(normalizeSearchTextCache.values)
	normalizeSearchTextCache.RUnlock()
	if entries != 1 || entries > normalizeSearchTextCacheMaxEntries {
		t.Fatalf("cache entries after crossing cap = %d, want 1 and at most %d", entries, normalizeSearchTextCacheMaxEntries)
	}
	if got, want := normalizeSearchText("entry-0"), "entry-0"; got != want {
		t.Fatalf("value after eviction = %q, want %q", got, want)
	}
}

func TestNormalizeSearchTextConcurrentCalls(t *testing.T) {
	resetNormalizeSearchTextCache()
	const workers, callsPerWorker = 16, 100
	inputs := []string{"Straße", "Cafe\u0301", "ΟΣ", "İ", "ı"}
	type result struct{ input, got string }
	results := make(chan result, workers*callsPerWorker)
	for worker := 0; worker < workers; worker++ {
		go func(offset int) {
			for i := 0; i < callsPerWorker; i++ {
				input := inputs[(offset+i)%len(inputs)]
				results <- result{input: input, got: normalizeSearchText(input)}
			}
		}(worker)
	}
	for i := 0; i < workers*callsPerWorker; i++ {
		result := <-results
		if want := normalizeSearchTextUncached(result.input); result.got != want {
			t.Errorf("concurrent normalize(%q) = %q, want %q", result.input, result.got, want)
		}
	}
}

func resetNormalizeSearchTextCache() {
	normalizeSearchTextCache.Lock()
	normalizeSearchTextCache.values = make(map[string]string)
	normalizeSearchTextCache.Unlock()
}

func BenchmarkNormalizeSearchText(b *testing.B) {
	values := make([]string, 0, 10000)
	for i := 0; i < 5000; i++ {
		values = append(values, fmt.Sprintf("User%04d Straße Café", i))
		values = append(values, fmt.Sprintf("Display %04d e\u0301 ΟΣ İ", i))
	}
	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			normalizeSearchTextUncached(values[i%len(values)])
		}
	})
	b.Run("memo-hit", func(b *testing.B) {
		resetNormalizeSearchTextCache()
		for _, value := range values {
			normalizeSearchText(value)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			normalizeSearchText(values[i%len(values)])
		}
	})
}
