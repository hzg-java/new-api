package billingexpr

import (
	"fmt"
	"math"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
)

// ScalePricingExpression materializes effective prices without changing usage,
// tier conditions, or request-rule factors. Canonical price leaves stay canonical.
func ScalePricingExpression(expression string, multiplier float64) (string, error) {
	if multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return "", fmt.Errorf("pricing multiplier must be finite and positive")
	}
	if _, err := CompileFromCache(expression); err != nil {
		return "", err
	}
	if multiplier == 1 {
		return expression, nil
	}
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return "", err
	}
	scaled, err := scalePriceTree(tree.Node, multiplier)
	if err != nil {
		return "", err
	}
	result := scaled.String()
	if strings.HasPrefix(expression, "v1:") {
		result = "v1:" + result
	}
	_, err = CompileFromCache(result)
	return result, err
}

func scalePriceTree(node ast.Node, multiplier float64) (ast.Node, error) {
	if conditional, ok := node.(*ast.ConditionalNode); ok {
		var err error
		conditional.Exp1, err = scalePriceTree(conditional.Exp1, multiplier)
		if err != nil {
			return nil, err
		}
		conditional.Exp2, err = scalePriceTree(conditional.Exp2, multiplier)
		return conditional, err
	}
	if call, ok := node.(*ast.CallNode); ok {
		if callee, ok := call.Callee.(*ast.IdentifierNode); ok && (callee.Value == "tier" || callee.Value == "fixed") {
			index := len(call.Arguments) - 1
			value, err := scalePriceTree(call.Arguments[index], multiplier)
			call.Arguments[index] = value
			return call, err
		}
	}
	if number, ok := requestRuleNumber(node); ok {
		value, _ := decimal.NewFromFloat(number).Mul(decimal.NewFromFloat(multiplier)).Float64()
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, fmt.Errorf("effective price is not finite")
		}
		return &ast.FloatNode{Value: value}, nil
	}
	if binary, ok := node.(*ast.BinaryNode); ok {
		var err error
		switch binary.Operator {
		case "+", "-":
			binary.Left, err = scalePriceTree(binary.Left, multiplier)
			if err != nil {
				return nil, err
			}
			binary.Right, err = scalePriceTree(binary.Right, multiplier)
			return binary, err
		case "*", "/":
			if containsPricingMarker(binary.Left) || binary.Operator == "/" {
				binary.Left, err = scalePriceTree(binary.Left, multiplier)
			} else {
				binary.Right, err = scalePriceTree(binary.Right, multiplier)
			}
			return binary, err
		}
	}
	// Arbitrary raw numeric programs need a value-level transformation: never
	// scale their internal thresholds, rounding arguments, or usage probes.
	return &ast.BinaryNode{Operator: "*", Left: node, Right: &ast.FloatNode{Value: multiplier}}, nil
}

// UsesFixedPricing includes unselected branches, even when compilation later
// optimizes them away. Hosts use it to reject unsupported billing entrances.
func UsesFixedPricing(expression string) bool {
	return UsesFixedPricingByHash(expression, ExprHashString(expression))
}

// UsesFixedPricingByHash avoids hashing an expression already in a snapshot.
func UsesFixedPricingByHash(expression, hash string) bool {
	entry, err := compileEntryFromCacheByHash(expression, hash)
	return err == nil && entry.fixedPricing
}

func containsPricingMarker(node ast.Node) bool {
	return ast.Find(node, func(part ast.Node) bool {
		identifier, ok := part.(*ast.IdentifierNode)
		return ok && (identifier.Value == "tier" || identifier.Value == "fixed")
	}) != nil
}

func isRequestPriceMultiplier(node ast.Node) bool {
	if identifier, ok := node.(*ast.IdentifierNode); ok && identifier.Value == "image_count" {
		return true
	}
	conditional, ok := node.(*ast.ConditionalNode)
	if !ok || !usesRequestProbe(conditional.Cond) || containsPricingMarker(conditional.Cond) {
		return false
	}
	multiplier, multiplierOK := requestRuleNumber(conditional.Exp1)
	fallback, fallbackOK := requestRuleNumber(conditional.Exp2)
	return multiplierOK && fallbackOK && fallback == 1 && multiplier >= 0 && !math.IsNaN(multiplier) && !math.IsInf(multiplier, 0)
}

// validateFixedPricingTree enforces one pricing leaf per execution. Without
// this invariant, adding two tiers or multiplying a fixed price by tokens
// would make both the request charge and its billing-unit trace ambiguous.
func validateFixedPricingTree(node ast.Node) error {
	switch part := node.(type) {
	case *ast.ConditionalNode:
		if containsPricingMarker(part.Cond) {
			break
		}
		if err := validateFixedPricingTree(part.Exp1); err != nil {
			return err
		}
		return validateFixedPricingTree(part.Exp2)
	case *ast.BinaryNode:
		if part.Operator != "*" {
			break
		}
		if isRequestPriceMultiplier(part.Right) {
			return validateFixedPricingTree(part.Left)
		}
		if isRequestPriceMultiplier(part.Left) {
			return validateFixedPricingTree(part.Right)
		}
	case *ast.CallNode:
		callee, ok := part.Callee.(*ast.IdentifierNode)
		if !ok || callee.Value != "tier" || len(part.Arguments) != 2 || containsPricingMarker(part.Arguments[0]) {
			break
		}
		price := part.Arguments[1]
		fixed, ok := price.(*ast.CallNode)
		if ok {
			function, direct := fixed.Callee.(*ast.IdentifierNode)
			if direct && function.Value == "fixed" && len(fixed.Arguments) == 1 {
				amount, literal := requestRuleNumber(fixed.Arguments[0])
				if literal && amount >= 0 && !math.IsNaN(amount) && !math.IsInf(amount*1_000_000, 0) {
					return nil
				}
				return fmt.Errorf("fixed price must be a finite, non-negative numeric literal with a finite v1 value")
			}
		}
		if !containsPricingMarker(price) {
			return nil
		}
	}
	return fmt.Errorf("fixed pricing requires tier(name, fixed(amount)) leaves, conditional tiers and request multipliers; token and fixed charges cannot be combined in one leaf")
}
