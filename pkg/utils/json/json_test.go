package json

import (
	"reflect"
	"testing"
)

func Test_removeFields(t *testing.T) {
	tests := []struct {
		name   string
		config any
		live   any
		want   any
	}{
		{
			name: "map",
			config: map[string]any{
				"foo": "bar",
			},
			live: map[string]any{
				"foo": "baz",
				"bar": "baz",
			},
			want: map[string]any{
				"foo": "baz",
			},
		},
		{
			name: "nested map",
			config: map[string]any{
				"foo": map[string]any{
					"bar": "baz",
				},
				"bar": "baz",
			},
			live: map[string]any{
				"foo": map[string]any{
					"bar": "qux",
					"baz": "qux",
				},
				"bar": "baz",
			},
			want: map[string]any{
				"foo": map[string]any{
					"bar": "qux",
				},
				"bar": "baz",
			},
		},
		{
			name: "list",
			config: []any{
				map[string]any{
					"foo": "bar",
				},
			},
			live: []any{
				map[string]any{
					"foo": "baz",
					"bar": "baz",
				},
			},
			want: []any{
				map[string]any{
					"foo": "baz",
				},
			},
		},
		{
			name: "nested list",
			config: []any{
				map[string]any{
					"foo": map[string]any{
						"bar": "baz",
					},
					"bar": "baz",
				},
			},
			live: []any{
				map[string]any{
					"foo": map[string]any{
						"bar": "qux",
						"baz": "qux",
					},
					"bar": "baz",
				},
			},
			want: []any{
				map[string]any{
					"foo": map[string]any{
						"bar": "qux",
					},
					"bar": "baz",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removeFields(tt.config, tt.live); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("removeFields() = %v, want %v", got, tt.want)
			}
		})
	}
}
