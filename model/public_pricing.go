package model

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
)

// PublicPricing deliberately does not embed Pricing: new internal fields must
// never become public implicitly.
type PublicPricing struct {
	ModelName              string                  `json:"model_name"`
	Description            string                  `json:"description,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	Tags                   string                  `json:"tags,omitempty"`
	VendorID               int                     `json:"vendor_id,omitempty"`
	EnableGroup            []string                `json:"enable_groups"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
	GroupPrices            map[string]PublicPrice  `json:"group_prices"`
}

type PublicPrice struct {
	BillingMode           string                               `json:"billing_mode"`
	BillingExpr           string                               `json:"billing_expr,omitempty"`
	QuotaType             int                                  `json:"quota_type"`
	BillingUsageSchema    map[string]jsplugin.UsageFieldSchema `json:"billing_usage_schema,omitempty"`
	BillingUsageExamples  []jsplugin.UsageExample              `json:"billing_usage_examples,omitempty"`
	BillingPluginVariants []PricingPluginVariant               `json:"billing_plugin_variants,omitempty"`
}

func PublicPricingForGroups(pricing []Pricing, groups map[string]float64, userIDs ...int) []PublicPricing {
	userID := 0
	if len(userIDs) > 0 {
		userID = userIDs[0]
	}
	result := make([]PublicPricing, 0, len(pricing))
	for _, item := range pricing {
		entry := PublicPricing{ModelName: item.ModelName, Description: item.Description, Icon: item.Icon, Tags: item.Tags, VendorID: item.VendorID, SupportedEndpointTypes: item.SupportedEndpointTypes, EnableGroup: []string{}, GroupPrices: map[string]PublicPrice{}}
		for group, factor := range groups {
			if !slices.Contains(item.EnableGroup, group) && !slices.Contains(item.EnableGroup, "all") {
				continue
			}
			price := PublicPrice{BillingMode: "tiered_expr", QuotaType: item.QuotaType, BillingUsageSchema: item.BillingUsageSchema, BillingUsageExamples: item.BillingUsageExamples}
			expression := item.BillingExpr
			if item.BillingMode != "tiered_expr" {
				expression = publicLegacyExpression(item, userID)
				if item.QuotaType == 1 {
					price.BillingUsageSchema, price.BillingUsageExamples = nil, nil
				}
			}
			price.BillingExpr = publicFinalExpression(expression, factor)
			for _, variant := range item.BillingPluginVariants {
				copy := variant
				expression := variant.BillingExpr
				if variant.BillingMode == "ratio" {
					expression = publicLegacyExpression(item, userID)
				}
				copy.BillingMode = "tiered_expr"
				if variant.BillingMode == "ratio" && item.QuotaType == 1 {
					copy.BillingUsageSchema, copy.BillingUsageExamples = nil, nil
				}
				copy.BillingExpr = publicFinalExpression(expression, factor)
				price.BillingPluginVariants = append(price.BillingPluginVariants, copy)
			}
			entry.EnableGroup = append(entry.EnableGroup, group)
			entry.GroupPrices[group] = price
		}
		slices.Sort(entry.EnableGroup)
		if len(entry.GroupPrices) > 0 {
			result = append(result, entry)
		}
	}
	return result
}

func publicLegacyExpression(item Pricing, userID int) string {
	if item.QuotaType == 1 {
		if !publicFinite(item.ModelPrice) {
			return ""
		}
		expression := fmt.Sprintf(`tier("request", fixed(%s))`, decimal.NewFromFloat(item.ModelPrice).String())
		if common.IsImageGenerationModel(item.ModelName) || slices.Contains(item.SupportedEndpointTypes, constant.EndpointTypeImageGeneration) {
			expression = fmt.Sprintf(`tier("image", fixed(%s)) * image_count`, decimal.NewFromFloat(item.ModelPrice).String())
			for _, rule := range legacyDallePricingRules(item.ModelName) {
				expression = "(" + expression + ") * (" + rule.Condition + " ? " + decimal.NewFromFloat(rule.Multiplier).String() + " : 1)"
			}
		}
		return expression
	}
	if !publicFinite(common.QuotaPerUnit) || common.QuotaPerUnit == 0 || !publicFinite(item.ModelRatio) || !publicFinite(item.CompletionRatio) {
		return ""
	}
	base := decimal.NewFromFloat(item.ModelRatio).Mul(decimal.NewFromInt(1000000)).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	terms := []string{"p * " + base.String(), "c * " + base.Mul(decimal.NewFromFloat(item.CompletionRatio)).String()}
	for _, lane := range []struct {
		name  string
		ratio *float64
	}{{"cr", item.CacheRatio}, {"cc", item.CreateCacheRatio}, {"img", item.ImageRatio}, {"ai", item.AudioRatio}} {
		if lane.ratio != nil && publicFinite(*lane.ratio) {
			terms = append(terms, lane.name+" * "+base.Mul(decimal.NewFromFloat(*lane.ratio)).String())
		}
	}
	if strings.Contains(strings.ToLower(item.ModelName), "claude") {
		write := ratio_setting.DefaultCreateCacheRatio
		if item.CreateCacheRatio != nil {
			write = *item.CreateCacheRatio
		} else {
			terms = append(terms, "cc * "+base.Mul(decimal.NewFromFloat(write)).String())
		}
		if !publicFinite(write) {
			return ""
		}
		terms = append(terms, "cc1h * "+base.Mul(decimal.NewFromFloat(write)).Mul(decimal.NewFromFloat(1.6)).String())
	}
	if item.AudioRatio != nil || item.AudioCompletionRatio != nil {
		input, output := 1.0, 1.0
		if item.AudioRatio != nil {
			input = *item.AudioRatio
		}
		if item.AudioCompletionRatio != nil {
			output = *item.AudioCompletionRatio
		}
		if item.AudioRatio == nil {
			terms = append(terms, "ai * "+base.String())
		}
		terms = append(terms, "ao * "+base.Mul(decimal.NewFromFloat(input)).Mul(decimal.NewFromFloat(output)).String())
	}
	if audioPrice := operation_setting.GetGeminiInputAudioPricePerMillionTokens(item.ModelName); audioPrice > 0 && item.AudioRatio == nil && item.AudioCompletionRatio == nil {
		price := decimal.NewFromFloat(audioPrice).Mul(decimal.NewFromFloat(ratio_setting.GetModelPricingMultiplier(item.ModelName))).Mul(decimal.NewFromFloat(ratio_setting.GetUserModelPricingMultiplier(userID, item.ModelName)))
		terms = append(terms, "ai * "+price.String())
	}
	return `tier("base", ` + strings.Join(terms, " + ") + `)`
}

func publicFinite(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Only canonical price trees are released. Never use ScalePricingExpression's
// arbitrary-program fallback here: it intentionally preserves private literals.
func publicFinalExpression(expression string, factor float64) string {
	if !publicFinite(factor) || expression == "" {
		return ""
	}
	_, body := billingexpr.ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return ""
	}
	result, ok := publicPriceTree(tree.Node, factor, 0)
	if !ok {
		return ""
	}
	return result
}

func publicNumber(node ast.Node) (float64, bool) {
	switch n := node.(type) {
	case *ast.IntegerNode:
		return float64(n.Value), n.Value >= 0
	case *ast.FloatNode:
		return n.Value, publicFinite(n.Value)
	}
	return 0, false
}

func publicCall(node ast.Node, name string) (*ast.CallNode, bool) {
	call, ok := node.(*ast.CallNode)
	if !ok {
		return nil, false
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	return call, ok && callee.Value == name
}

func publicCondition(node ast.Node) bool {
	if call, ok := publicCall(node, "u"); ok && len(call.Arguments) == 1 {
		_, ok := call.Arguments[0].(*ast.StringNode)
		return ok
	}
	if binary, ok := node.(*ast.BinaryNode); ok {
		if binary.Operator == "&&" || binary.Operator == "||" {
			return publicCondition(binary.Left) && publicCondition(binary.Right)
		}
		if !slices.Contains([]string{"==", "!=", "<=", ">=", "<", ">"}, binary.Operator) {
			return false
		}
		if id, ok := binary.Left.(*ast.IdentifierNode); ok && id.Value == "len" {
			_, ok := publicNumber(binary.Right)
			return ok
		}
		for _, name := range []string{"hour", "minute", "weekday", "month", "day"} {
			if call, ok := publicCall(binary.Left, name); ok && len(call.Arguments) == 1 {
				if _, ok := call.Arguments[0].(*ast.StringNode); !ok {
					return false
				}
				number, ok := publicNumber(binary.Right)
				return ok && number <= 60
			}
		}
		for _, name := range []string{"u", "param", "header"} {
			if call, ok := publicCall(binary.Left, name); ok && len(call.Arguments) == 1 {
				if _, ok := call.Arguments[0].(*ast.StringNode); !ok {
					return false
				}
				if binary.Operator != "==" && binary.Operator != "!=" {
					return false
				}
				switch binary.Right.(type) {
				case *ast.StringNode, *ast.BoolNode:
					return true
				}
			}
		}
	}
	return false
}

func publicPriceTree(node ast.Node, factor float64, depth int) (string, bool) {
	if depth > 16 || !publicFinite(factor) {
		return "", false
	}
	if conditional, ok := node.(*ast.ConditionalNode); ok {
		if !publicCondition(conditional.Cond) {
			return "", false
		}
		yes, y := publicPriceTree(conditional.Exp1, factor, depth+1)
		no, n := publicPriceTree(conditional.Exp2, factor, depth+1)
		return conditional.Cond.String() + " ? " + yes + " : " + no, y && n
	}
	if binary, ok := node.(*ast.BinaryNode); ok && binary.Operator == "*" {
		if id, ok := binary.Right.(*ast.IdentifierNode); ok && id.Value == "image_count" {
			left, ok := publicPriceTree(binary.Left, factor, depth+1)
			return "(" + left + ") * image_count", ok
		}
		if number, ok := publicNumber(binary.Right); ok {
			return publicPriceTree(binary.Left, factor*number, depth+1)
		}
		if conditional, ok := binary.Right.(*ast.ConditionalNode); ok && publicCondition(conditional.Cond) {
			yes, y := publicNumber(conditional.Exp1)
			no, n := publicNumber(conditional.Exp2)
			if !y || !n {
				return "", false
			}
			left, l := publicPriceTree(binary.Left, factor*yes, depth+1)
			right, r := publicPriceTree(binary.Left, factor*no, depth+1)
			return conditional.Cond.String() + " ? " + left + " : " + right, l && r
		}
	}
	if call, ok := publicCall(node, "tier"); ok && len(call.Arguments) == 2 {
		if _, ok := call.Arguments[0].(*ast.StringNode); !ok {
			return "", false
		}
		if binary, ok := call.Arguments[1].(*ast.BinaryNode); ok && binary.Operator == "*" {
			if conditional, ok := binary.Right.(*ast.ConditionalNode); ok && publicCondition(conditional.Cond) {
				left := &ast.CallNode{Callee: call.Callee, Arguments: []ast.Node{call.Arguments[0], &ast.BinaryNode{Operator: "*", Left: binary.Left, Right: conditional.Exp1}}}
				right := &ast.CallNode{Callee: call.Callee, Arguments: []ast.Node{call.Arguments[0], &ast.BinaryNode{Operator: "*", Left: binary.Left, Right: conditional.Exp2}}}
				return publicPriceTree(&ast.ConditionalNode{Cond: conditional.Cond, Exp1: left, Exp2: right}, factor, depth+1)
			}
		}
		body, ok := publicPriceBody(call.Arguments[1], factor)
		return "tier(" + call.Arguments[0].String() + ", " + body + ")", ok
	}
	body, ok := publicPriceBody(node, factor)
	return `tier("base", ` + body + `)`, ok
}

func publicPriceBody(node ast.Node, factor float64) (string, bool) {
	if number, ok := publicNumber(node); ok {
		value := number * factor
		if !publicFinite(value) {
			return "", false
		}
		return decimal.NewFromFloat(number).Mul(decimal.NewFromFloat(factor)).String(), true
	}
	if call, ok := publicCall(node, "fixed"); ok && len(call.Arguments) == 1 {
		if _, ok := publicNumber(call.Arguments[0]); !ok {
			return "", false
		}
		value, ok := publicPriceBody(call.Arguments[0], factor)
		return "fixed(" + value + ")", ok
	}
	binary, ok := node.(*ast.BinaryNode)
	if !ok {
		return "", false
	}
	if binary.Operator == "+" {
		left, l := publicPriceBody(binary.Left, factor)
		right, r := publicPriceBody(binary.Right, factor)
		return left + " + " + right, l && r
	}
	if binary.Operator == "/" {
		if divisor, ok := publicNumber(binary.Right); ok && divisor == 1000000 {
			left, ok := publicPriceBody(binary.Left, factor)
			return left + " / 1000000", ok
		}
		return "", false
	}
	if binary.Operator != "*" {
		return "", false
	}
	if _, ok := publicNumber(binary.Right); !ok {
		return "", false
	}
	valid := false
	if id, ok := binary.Left.(*ast.IdentifierNode); ok {
		valid = slices.Contains([]string{"p", "c", "cr", "cc", "cc1h", "img", "img_cr", "img_o", "ai", "ao"}, id.Value)
	}
	if call, ok := publicCall(binary.Left, "u"); ok && len(call.Arguments) == 1 {
		_, valid = call.Arguments[0].(*ast.StringNode)
	}
	if !valid {
		return "", false
	}
	value, ok := publicPriceBody(binary.Right, factor)
	return binary.Left.String() + " * " + value, ok
}
