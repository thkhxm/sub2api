//go:build unit

package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCliModelContextIncludesGPT6AndPreservesExistingBudgets(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"gpt-6", 922000},
		{"gpt-6-astra", 922000},
		{"gpt-6-astra-2026-09-04", 922000},
		{"gpt-5.6-sol", 272000},
		{"gpt-image-2", 0},
		{"gpt-4o", 128000},
		{"custom-model", 128000},
	} {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.want, contextWindowForCliModel(tc.model))
		})
	}
}
