package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScalePricingExpression(t *testing.T) {
	for _, tc := range []struct{ expression, want string }{
		{`len <= 200000 ? tier("short", p * 2 + c * 8) : tier("long", p * 4)`, `len <= 200000 ? tier("short", p * 3 + c * 12) : tier("long", p * 6)`},
		{`tier("request", fixed(0.02)) * image_count`, `tier("request", fixed(0.03)) * image_count`},
		{`tier("base", 0.1 + u("tokens") * 8 / 1000000 + u("seconds") * 2)`, `tier("base", 0.15 + u("tokens") * 12 / 1000000 + u("seconds") * 3)`},
		{`tier("raw", max(p - 10, 0) * (len > 100 ? 2 : 1))`, `tier("raw", max(p - 10, 0) * (len > 100 ? 3 : 1.5))`},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got, err := ScalePricingExpression(tc.expression, 1.5)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			original, trace, err := RunExprWithRequest(tc.expression, TokenParams{P: 500, C: 200, Len: 500}, RequestInput{Usage: map[string]any{"tokens": 1000.0, "seconds": 3.0}})
			require.NoError(t, err)
			scaled, scaledTrace, err := RunExprWithRequest(got, TokenParams{P: 500, C: 200, Len: 500}, RequestInput{Usage: map[string]any{"tokens": 1000.0, "seconds": 3.0}})
			require.NoError(t, err)
			assert.InDelta(t, original*1.5, scaled, 1e-9)
			assert.Equal(t, trace.MatchedTier, scaledTrace.MatchedTier)
		})
	}
}

func TestTaskUsageExpressionUsesFactsAndTaskQuotaConversion(t *testing.T) {
	expression := `tier("1080p", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2))`
	cost, trace, err := RunExprWithRequest(expression, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, 4.0, cost)
	assert.Equal(t, "1080p", trace.MatchedTier)
	result, err := ComputeTieredQuotaWithRequest(&BillingSnapshot{ExprString: expression, ExprHash: ExprHashString(expression), GroupRatio: 2, QuotaPerUnit: 500000, ExprVersion: 1, TaskUsageBilling: true}, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, 4_000_000, result.ActualQuotaAfterGroup)
}
