package main

import "testing"

func TestValidateNewProduct(t *testing.T) {
	long := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}
	cases := []struct {
		name    string
		id      string
		pname   string
		sku     string
		price   float64
		stock   int
		wantBad bool
	}{
		{"good", "kb-001", "Keyboard", "SKU-1", 25, 10, false},
		{"zero stock is fine", "kb-001", "Keyboard", "SKU-1", 25, 0, false},
		{"negative price", "kb-001", "Keyboard", "SKU-1", -5, 10, true},
		{"zero price", "kb-001", "Keyboard", "SKU-1", 0, 10, true},
		{"negative stock", "kb-001", "Keyboard", "SKU-1", 25, -1, true},
		{"blank id", "  ", "Keyboard", "SKU-1", 25, 10, true},
		{"blank name", "kb-001", "", "SKU-1", 25, 10, true},
		{"blank sku", "kb-001", "Keyboard", "", 25, 10, true},
		{"id too long", long(51), "Keyboard", "SKU-1", 25, 10, true},
		{"price too large", "kb-001", "Keyboard", "SKU-1", 1e9, 10, true},
	}
	for _, c := range cases {
		got := validateNewProduct(c.id, c.pname, c.sku, c.price, c.stock)
		if (got != "") != c.wantBad {
			t.Errorf("%s: got %q, wantBad=%v", c.name, got, c.wantBad)
		}
	}
}
