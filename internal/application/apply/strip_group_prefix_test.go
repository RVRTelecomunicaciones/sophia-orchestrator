package apply

import (
	"reflect"
	"testing"
)

func TestStripGroupPrefix(t *testing.T) {
	tests := []struct {
		name      string
		groupName string
		patterns  []string
		want      []string
	}{
		{
			name:      "strips redundant group prefix (the double-nesting bug)",
			groupName: "backend",
			patterns:  []string{"backend/go.mod", "backend/internal/domain/cart.go"},
			want:      []string{"go.mod", "internal/domain/cart.go"},
		},
		{
			name:      "frontend group prefix stripped",
			groupName: "frontend",
			patterns:  []string{"frontend/package.json", "frontend/src/main.ts"},
			want:      []string{"package.json", "src/main.ts"},
		},
		{
			name:      "patterns without the prefix are untouched",
			groupName: "backend",
			patterns:  []string{"go.mod", "internal/store/cart_store.go"},
			want:      []string{"go.mod", "internal/store/cart_store.go"},
		},
		{
			name:      "group name deeper in the path is NOT stripped",
			groupName: "domain",
			patterns:  []string{"internal/domain/product.go"},
			want:      []string{"internal/domain/product.go"},
		},
		{
			name:      "only an exact leading segment is stripped, not a name substring",
			groupName: "back",
			patterns:  []string{"backend/go.mod"},
			want:      []string{"backend/go.mod"},
		},
		{
			name:      "empty patterns",
			groupName: "backend",
			patterns:  []string{},
			want:      []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripGroupPrefix(tt.groupName, tt.patterns)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stripGroupPrefix(%q, %v) = %v, want %v", tt.groupName, tt.patterns, got, tt.want)
			}
		})
	}
}
