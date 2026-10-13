/**
 * Type definitions for the test suite
 *
 * Provides properly typed helpers for working with Neon query results.
 */

import type { FullQueryResults, NeonQueryFunction } from "@neondatabase/serverless";

/**
 * A single row returned from a query (object mode)
 */
export type Row = Record<string, unknown>;

/**
 * Array of rows returned from a query (object mode, no fullResults)
 */
export type Rows = Row[];

/**
 * A single row in array mode
 */
export type ArrayRow = unknown[];

/**
 * Array of rows in array mode
 */
export type ArrayRows = ArrayRow[];

/**
 * Full query results with metadata (object mode)
 */
export type FullResult = FullQueryResults<false>;

/**
 * Full query results with metadata (array mode)
 */
export type FullResultArray = FullQueryResults<true>;

/**
 * SQL function type that returns rows directly (default neon behavior)
 */
export type SqlFunction = NeonQueryFunction<false, false>;

/**
 * SQL function type that returns full results with metadata
 */
export type SqlFunctionFull = NeonQueryFunction<false, true>;

/**
 * Helper to assert result is an array of rows
 * Use when you know the query returns rows (not fullResults)
 */
export function asRows<T extends Row = Row>(result: unknown): T[] {
  return result as T[];
}

/**
 * Helper to assert result is full query results
 */
export function asFullResult(result: unknown): FullResult {
  return result as FullResult;
}

/**
 * Type guard to check if result is FullQueryResults
 */
export function isFullResult(result: unknown): result is FullQueryResults<boolean> {
  return (
    result !== null &&
    typeof result === "object" &&
    "rows" in result &&
    "fields" in result &&
    "command" in result
  );
}
