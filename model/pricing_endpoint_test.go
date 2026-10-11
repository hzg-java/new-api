package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonalPricingDoesNotMutateSharedPrices(t *testing.T) {
	previous := ratio_setting.UserModelPricingMultiplier2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateUserModelPricingMultiplierByJSONString(previous)) })
	require.NoError(t, ratio_setting.UpdateUserModelPricingMultiplierByJSONString(`{"101::personal-model":0.8}`))
	shared := []Pricing{{ModelName: "personal-model", ModelPrice: 1.5, BillingExpr: `tier("base", p * 3)`, BillingPluginVariants: []PricingPluginVariant{{BillingExpr: `tier("video", u("seconds") * 0.3)`}}}}
	personal, err := PricingForUser(shared, 101)
	require.NoError(t, err)
	assert.InDelta(t, 1.2, personal[0].ModelPrice, 1e-12)
	assert.Equal(t, `tier("base", p * 2.4)`, personal[0].BillingExpr)
	assert.Equal(t, `tier("video", u("seconds") * 0.24)`, personal[0].BillingPluginVariants[0].BillingExpr)
	other, err := PricingForUser(shared, 102)
	require.NoError(t, err)
	assert.Equal(t, shared, other)
	assert.Equal(t, 1.5, shared[0].ModelPrice)
	assert.Equal(t, `tier("video", u("seconds") * 0.3)`, shared[0].BillingPluginVariants[0].BillingExpr)
}

func TestPublicPricingFinalPricesAndPrivacy(t *testing.T) {
	cache, audio, audioOut := 0.25, 4.0, 2.0
	shared := []Pricing{{ModelName: "public-model", EnableGroup: []string{"all"}, ModelRatio: 1.5, CompletionRatio: 3, CacheRatio: &cache, AudioRatio: &audio, AudioCompletionRatio: &audioOut}}
	public := PublicPricingForGroups(shared, map[string]float64{"default": 2, "free": 0})
	require.Len(t, public, 1)
	encoded, err := common.Marshal(public)
	require.NoError(t, err)
	for _, forbidden := range []string{"model_ratio", "completion_ratio", "cache_ratio", "audio_ratio", "group_ratio", "model_price"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
	assert.Contains(t, public[0].GroupPrices["default"].BillingExpr, "p * 6")
	assert.Contains(t, public[0].GroupPrices["default"].BillingExpr, "c * 18")
	assert.Contains(t, public[0].GroupPrices["default"].BillingExpr, "ai * 24")
	assert.Contains(t, public[0].GroupPrices["default"].BillingExpr, "ao * 48")
	assert.Contains(t, public[0].GroupPrices["free"].BillingExpr, "p * 0")
	assert.Equal(t, 1.5, shared[0].ModelRatio)
	private := PublicPricingForGroups([]Pricing{{ModelName: "restricted", EnableGroup: []string{"hidden"}}}, map[string]float64{"default": 1})
	assert.Empty(t, private)
	fixed := PublicPricingForGroups([]Pricing{{ModelName: "task", QuotaType: 1, ModelPrice: 0.3, EnableGroup: []string{"default"}, BillingPluginVariants: []PricingPluginVariant{{PluginKey: "fallback", BillingMode: "ratio"}}}}, map[string]float64{"default": 2})
	assert.Equal(t, `tier("request", fixed(0.6))`, fixed[0].GroupPrices["default"].BillingPluginVariants[0].BillingExpr)
}

func TestPublicPricingCanonicalExpressions(t *testing.T) {
	for _, tc := range []struct{ name, expression, expected string }{
		{"token tiers", `len <= 200000 ? tier("short", p * 3 + c * 15) : tier("long", p * 6 + c * 22.5)`, `len <= 200000 ? tier("short", p * 6 + c * 30) : tier("long", p * 12 + c * 45)`},
		{"fixed", `tier("request", fixed(0.1))`, `tier("request", fixed(0.2))`},
		{"image quantity", `tier("image", fixed(0.1)) * image_count`, `(tier("image", fixed(0.2))) * image_count`},
		{"task tokens", `tier("video", u("tokens") * 9.8 / 1000000)`, `tier("video", u("tokens") * 19.6 / 1000000)`},
		{"nested task prices", `tier("video", u("seconds") * (u("resolution") == "4K" ? 0.12 : 0.09))`, `u("resolution") == "4K" ? tier("video", u("seconds") * 0.24) : tier("video", u("seconds") * 0.18)`},
		{"time prices", `hour("UTC") >= 18 ? tier("peak", p * 3) : tier("base", p * 2)`, `hour("UTC") >= 18 ? tier("peak", p * 6) : tier("base", p * 4)`},
		{"unsafe program", `tier("secret", max(p, 12345) * 0.2)`, ""},
		{"unsafe request threshold", `param("private") > 12345 ? tier("a", p * 2) : tier("b", p * 3)`, ""},
		{"unsafe bare request probe", `param("private") ? tier("a", p * 2) : tier("b", p * 3)`, ""},
		{"unsafe dynamic usage key", `u(param("private")) ? tier("a", p * 2) : tier("b", p * 3)`, ""},
		{"unsafe usage probe arity", `u("has_video_input", "private") ? tier("a", p * 2) : tier("b", p * 3)`, ""},
		{"unsafe boolean branch body", `u("has_video_input") ? tier("secret", max(p, 12345) * 0.2) : tier("b", p * 3)`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public := PublicPricingForGroups([]Pricing{{ModelName: "test", EnableGroup: []string{"default"}, BillingMode: "tiered_expr", BillingExpr: tc.expression}}, map[string]float64{"default": 2})
			require.Len(t, public, 1)
			assert.Equal(t, tc.expected, public[0].GroupPrices["default"].BillingExpr)
		})
	}
	expression := `tier("base", p * 3) * (param("service_tier") == "fast" ? 6 : 1)`
	public := PublicPricingForGroups([]Pricing{{ModelName: "test", EnableGroup: []string{"default"}, BillingMode: "tiered_expr", BillingExpr: expression, BillingPluginVariants: []PricingPluginVariant{{PluginKey: "video", BillingMode: "tiered_expr", BillingExpr: `tier("base", u("seconds") * 0.4)`}}}}, map[string]float64{"default": 2})
	price := public[0].GroupPrices["default"]
	assert.Contains(t, price.BillingExpr, "p * 36")
	assert.Contains(t, price.BillingExpr, "p * 6")
	assert.NotContains(t, price.BillingExpr, "? 6 : 1")
	assert.Equal(t, `tier("base", u("seconds") * 0.8)`, price.BillingPluginVariants[0].BillingExpr)
}

func TestPublicPricingSeedanceBooleanUsage(t *testing.T) {
	previous := ratio_setting.UserModelPricingMultiplier2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateUserModelPricingMultiplierByJSONString(previous)) })
	require.NoError(t, ratio_setting.UpdateUserModelPricingMultiplierByJSONString(`{"101::seedance":0.8}`))

	for _, tc := range []struct{ name, expression, grouped, personal string }{
		{
			"video input",
			`u("has_video_input") ? tier("sd_video_in", u("tokens") * 22 / 1000000) : tier("sd", u("tokens") * 37 / 1000000)`,
			`u("has_video_input") ? tier("sd_video_in", u("tokens") * 44 / 1000000) : tier("sd", u("tokens") * 74 / 1000000)`,
			`u("has_video_input") ? tier("sd_video_in", u("tokens") * 52.8 / 1000000) : tier("sd", u("tokens") * 88.8 / 1000000)`,
		},
		{
			"nested resolution",
			`u("resolution") == "1080p" ? (u("has_video_input") ? tier("hd_video_in", u("tokens") * 44 / 1000000) : tier("hd", u("tokens") * 74 / 1000000)) : (u("has_video_input") ? tier("sd_video_in", u("tokens") * 22 / 1000000) : tier("sd", u("tokens") * 37 / 1000000))`,
			`u("resolution") == "1080p" ? u("has_video_input") ? tier("hd_video_in", u("tokens") * 88 / 1000000) : tier("hd", u("tokens") * 148 / 1000000) : u("has_video_input") ? tier("sd_video_in", u("tokens") * 44 / 1000000) : tier("sd", u("tokens") * 74 / 1000000)`,
			`u("resolution") == "1080p" ? u("has_video_input") ? tier("hd_video_in", u("tokens") * 105.6 / 1000000) : tier("hd", u("tokens") * 177.6 / 1000000) : u("has_video_input") ? tier("sd_video_in", u("tokens") * 52.8 / 1000000) : tier("sd", u("tokens") * 88.8 / 1000000)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shared := []Pricing{{ModelName: "seedance", EnableGroup: []string{"default"}, BillingMode: "tiered_expr", BillingExpr: tc.expression, BillingPluginVariants: []PricingPluginVariant{{PluginKey: "doubao", BillingMode: "tiered_expr", BillingExpr: tc.expression}}}}
			public := PublicPricingForGroups(shared, map[string]float64{"default": 2})
			require.Len(t, public, 1)
			price := public[0].GroupPrices["default"]
			assert.Equal(t, tc.grouped, price.BillingExpr)
			require.Len(t, price.BillingPluginVariants, 1)
			assert.Equal(t, tc.grouped, price.BillingPluginVariants[0].BillingExpr)

			// Shared pricing already carries the default model multiplier before personalization.
			defaultExpression, err := billingexpr.ScalePricingExpression(tc.expression, 1.5)
			require.NoError(t, err)
			shared[0].BillingExpr = defaultExpression
			shared[0].BillingPluginVariants[0].BillingExpr = defaultExpression
			personal, err := PricingForUser(shared, 101)
			require.NoError(t, err)
			public = PublicPricingForGroups(personal, map[string]float64{"default": 2}, 101)
			require.Len(t, public, 1)
			price = public[0].GroupPrices["default"]
			assert.Equal(t, tc.personal, price.BillingExpr)
			require.Len(t, price.BillingPluginVariants, 1)
			assert.Equal(t, tc.personal, price.BillingPluginVariants[0].BillingExpr)
			encoded, err := common.Marshal(public)
			require.NoError(t, err)
			for _, forbidden := range []string{"model_ratio", "group_ratio", "model_price", "multiplier"} {
				assert.NotContains(t, string(encoded), forbidden)
			}
			assert.Equal(t, defaultExpression, shared[0].BillingExpr)
			assert.Equal(t, defaultExpression, shared[0].BillingPluginVariants[0].BillingExpr)
		})
	}
}

func resetPricingEndpointTestTables(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &Vendor{}))
	for _, table := range []string{"abilities", "channels", "models", "vendors"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	InitChannelCache()
	InvalidatePricingCache()
	t.Cleanup(func() {
		for _, table := range []string{"abilities", "channels", "models", "vendors"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
		InitChannelCache()
		InvalidatePricingCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

func insertPricingEndpointChannel(t *testing.T, channelID int, channelType int, settings dto.ChannelOtherSettings) {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   channelType,
		Key:    fmt.Sprintf("key-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", channelID),
	}
	if settings.AdvancedCustom != nil {
		channel.SetOtherSettings(settings)
	}
	require.NoError(t, DB.Create(channel).Error)
}

func insertPricingEndpointAbility(t *testing.T, channelID int, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
}

func pricingEndpointAdvancedCustomConfig(routes ...dto.AdvancedCustomRoute) dto.ChannelOtherSettings {
	return dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: routes,
		},
	}
}

func pricingEndpointTypesByModel(t *testing.T) map[string][]constant.EndpointType {
	t.Helper()
	InitChannelCache()
	return pricingEndpointTypesFromPricing(GetPricing())
}

func pricingEndpointTypesFromPricing(pricings []Pricing) map[string][]constant.EndpointType {
	byModel := make(map[string][]constant.EndpointType)
	for _, pricing := range pricings {
		byModel[pricing.ModelName] = pricing.SupportedEndpointTypes
	}
	return byModel
}

func TestPricingAdvancedCustomUsesConfiguredEndpointTypes(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 101, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 101, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 101, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
	}, byModel["gpt-4o"])
}

func TestPricingModelMetadataEndpointsMergeWithAdvancedCustomInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 103, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 103, "gemini-2.5-flash")
	require.NoError(t, DB.Create(&Model{
		ModelName: "gemini-2.5-flash",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAI,
	}, byModel["gemini-2.5-flash"])
}

func TestPricingModelMetadataEndpointsCanProvideEndpointWithoutChannelInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 104, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 104, "metadata-only-model")
	require.NoError(t, DB.Create(&Model{
		ModelName: "metadata-only-model",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["metadata-only-model"])
}

func TestPricingAdvancedCustomMissingConfigFallsBackToChannelType(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 102, constant.ChannelTypeAdvancedCustom, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 102, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
}

func TestPricingNativeChannelEndpointTypesUnchanged(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 201, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 202, constant.ChannelTypeGemini, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 203, constant.ChannelTypeAnthropic, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 201, "gpt-4o")
	insertPricingEndpointAbility(t, 202, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 203, "claude-3-5-sonnet")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}, byModel["claude-3-5-sonnet"])
}

func TestInitChannelCacheInvalidatesPricingCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 301, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
	))
	insertPricingEndpointAbility(t, 301, "gemini-3.5-flash")
	InitChannelCache()

	initial := pricingEndpointTypesByModel(t)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, initial["gemini-3.5-flash"])

	var channel Channel
	require.NoError(t, DB.First(&channel, "id = ?", 301).Error)
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 301).Update("settings", channel.OtherSettings).Error)
	InitChannelCache()

	updated := pricingEndpointTypesByModel(t)
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, updated["gemini-3.5-flash"])
}

func TestInitChannelCacheInvalidatesStartupPricingBuiltBeforeChannelCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 302, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 302, "gemini-3.5-flash")

	staleByModel := pricingEndpointTypesFromPricing(GetPricing())
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, staleByModel["gemini-3.5-flash"])

	InitChannelCache()

	rebuiltByModel := pricingEndpointTypesFromPricing(GetPricing())
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, rebuiltByModel["gemini-3.5-flash"])
}

func TestCacheUpdateChannelSyncsAdvancedCustomConfig(t *testing.T) {
	resetPricingEndpointTestTables(t)

	channel := &Channel{
		Id:     401,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "key-401",
		Status: common.ChannelStatusEnabled,
		Name:   "channel-401",
	}
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/responses",
		UpstreamPath: "/v1beta/models/{model}:generateContent",
		Converter:    "openai_responses_to_gemini_generate_content",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIResponse}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.Type = constant.ChannelTypeOpenAI
	CacheUpdateChannel(channel)

	assert.Nil(t, channel2advancedCustomConfig[401])
}
