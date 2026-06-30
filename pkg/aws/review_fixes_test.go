package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitAndTrim(t *testing.T) {
	const a, b = "sg-1", "sg-2"
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "single", in: a, want: []string{a}},
		{name: "spaces", in: a + ", " + b, want: []string{a, b}},
		{name: "trailing comma", in: a + ",", want: []string{a}},
		{name: "empty segments", in: a + ",," + b, want: []string{a, b}},
		{name: "whitespace only", in: "  ", want: nil},
		{name: "empty", in: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitAndTrim(tt.in))
		})
	}
}

func TestIsArmFamily(t *testing.T) {
	arm := []string{"a1", "m6g", "c7gn", "m6gd", "x2gd", "im4gn", "is4gen", "r8g", "t4g"}
	for _, f := range arm {
		assert.True(t, isArmFamily(f), "%s should be arm", f)
	}
	x86 := []string{"c5", "m5", "t3", "t3a", "c6i", "m7i", "r6a", "c5n", "m5dn", "i3"}
	for _, f := range x86 {
		assert.False(t, isArmFamily(f), "%s should be x86", f)
	}
}
