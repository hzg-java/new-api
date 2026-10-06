/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { BillingUsageSchema } from '../types'
import {
  parseTaskTiersFromExpr,
  splitBillingExprAndRequestRules,
  type ParsedTaskTier,
  type TaskTierCondition,
} from './billing-expr'
import { compileBillingExpression } from './billing-expression/parser'
import { readConditionalTaskPricing } from './billing-expression/task-display'
import { visitExpression } from './billing-expression/types'
import {
  getTaskEnumFields,
  taskMatrixRowLabel,
  tryParseTaskMatrixConfig,
} from './task-expr'

export function getTaskMatrixDisplayTiers(
  expression: string | null | undefined,
  schema: BillingUsageSchema | null | undefined
): ParsedTaskTier[] | null {
  if (!schema) return null
  if (
    getTaskEnumFields(schema).length === 0 &&
    !Object.values(schema).some((definition) => definition.type === 'boolean')
  ) {
    return null
  }

  const matrix = tryParseTaskMatrixConfig(expression, schema)
  let tiers: ParsedTaskTier[]
  if (!matrix) {
    const { billingExpr } = splitBillingExprAndRequestRules(expression || '')
    const conditionalTiers = readConditionalTaskPricing(billingExpr, schema)
    if (
      !conditionalTiers?.length ||
      conditionalTiers.some((tier) => tier.conditionText) ||
      conditionalTiers.every((tier) => tier.conditions.length === 0)
    ) {
      return null
    }
    tiers = conditionalTiers
  } else {
    tiers = matrix.rows.map((row) => ({
      label: taskMatrixRowLabel(row.combination),
      conditions: Object.entries(row.combination)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([field, value]) => ({ field, value })),
      constant: row.constant,
      unitPrices: { ...row.unitPrices },
    }))
  }

  const resolutionFields = new Set(
    ['resolution', 'resolution_tier'].filter((field) =>
      schema[field]?.enum?.some((value) => value.toLowerCase() === '4k')
    )
  )
  if (resolutionFields.size === 0) return tiers

  const { billingExpr } = splitBillingExprAndRequestRules(expression || '')
  const compiled = compileBillingExpression(billingExpr)
  if (compiled.status !== 'ready') return tiers

  let hasResolutionCondition = false
  let hasExplicit4k = false
  visitExpression(compiled.ast, (node) => {
    if (node.kind !== 'binary' || !['==', '!='].includes(node.operator)) return
    for (const [probe, literal] of [
      [node.left, node.right],
      [node.right, node.left],
    ]) {
      if (probe.kind !== 'call' || probe.name !== 'u') continue
      const key = probe.args[0]
      if (
        key?.kind !== 'literal' ||
        typeof key.value !== 'string' ||
        !resolutionFields.has(key.value) ||
        literal.kind !== 'literal' ||
        typeof literal.value !== 'string'
      ) {
        continue
      }
      hasResolutionCondition = true
      if (literal.value.toLowerCase() === '4k') hasExplicit4k = true
    }
  })
  if (!hasResolutionCondition || hasExplicit4k) return tiers
  return tiers.filter(
    (tier) =>
      !tier.conditions.some(
        (condition) =>
          resolutionFields.has(condition.field) &&
          condition.value.toLowerCase() === '4k'
      )
  )
}

/** Display explicit conditions for a fallback only when its complement is unique.
 * Unlike the editor matrix, unrelated schema fields do not expand the price table.
 */
export function getTaskPricingDisplayTiers(
  expression: string | null | undefined,
  schema: BillingUsageSchema | null | undefined
): ParsedTaskTier[] {
  const tiers = parseTaskTiersFromExpr(expression || '', schema, true)
  if (tiers.length === 0 && expression && schema) {
    const { billingExpr } = splitBillingExprAndRequestRules(expression)
    return readConditionalTaskPricing(billingExpr, schema) ?? []
  }
  const fallback = tiers.at(-1)
  if (!schema || tiers.length < 2 || !fallback) return tiers
  const previous = tiers.slice(0, -1)
  const fields = [
    ...new Set(
      previous.flatMap((tier) =>
        tier.conditions.map((condition) => condition.field)
      )
    ),
  ].sort()
  let combinations: TaskTierCondition[][] = [[]]
  for (const field of fields) {
    const definition = schema[field]
    const values =
      definition?.type === 'boolean' ? ['false', 'true'] : definition?.enum
    // Avoid expanding large plugin schemas merely to name a fallback row.
    if (!values?.length || combinations.length * values.length > 256) {
      return tiers
    }
    combinations = combinations.flatMap((combination) =>
      values.map((value) => [...combination, { field, value }])
    )
  }
  const remaining = combinations.filter(
    (combination) =>
      !previous.some((tier) =>
        tier.conditions.every((condition) =>
          combination.some(
            (value) =>
              value.field === condition.field && value.value === condition.value
          )
        )
      )
  )
  if (remaining.length !== 1) return tiers
  return [...previous, { ...fallback, conditions: remaining[0] }]
}
