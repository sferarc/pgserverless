/**
 * Custom assertions for the test suite
 *
 * Provides specialized matchers for comparing database results,
 * error structures, and type-specific values.
 */

import type { FieldDef, FullQueryResults } from "@neondatabase/serverless";
import { expect } from "vitest";

/**
 * Assert that two result sets are equivalent
 */
export function assertResultsEqual(
  actual: unknown,
  expected: unknown,
  options: {
    ignoreOrder?: boolean;
    ignoreFieldOids?: boolean;
    floatTolerance?: number;
  } = {},
): void {
  const { ignoreOrder = false, ignoreFieldOids = true, floatTolerance = 1e-10 } = options;

  // Handle array results
  if (Array.isArray(actual) && Array.isArray(expected)) {
    expect(actual.length).toBe(expected.length);

    if (ignoreOrder) {
      // Sort both arrays for comparison
      const sortedActual = [...actual].sort(rowComparator);
      const sortedExpected = [...expected].sort(rowComparator);
      for (let i = 0; i < sortedActual.length; i++) {
        assertRowsEqual(sortedActual[i], sortedExpected[i], floatTolerance);
      }
    } else {
      for (let i = 0; i < actual.length; i++) {
        assertRowsEqual(actual[i], expected[i], floatTolerance);
      }
    }
    return;
  }

  // Handle full results
  const actualObj = actual as Record<string, unknown>;
  const expectedObj = expected as Record<string, unknown>;
  if (actualObj?.rows !== undefined && expectedObj?.rows !== undefined) {
    assertResultsEqual(actualObj.rows, expectedObj.rows, options);

    if (actualObj.command !== undefined) {
      expect(actualObj.command as string).toBe(expectedObj.command as string);
    }

    if (actualObj.rowCount !== undefined) {
      expect(actualObj.rowCount as number).toBe(expectedObj.rowCount as number);
    }

    if (actualObj.fields !== undefined && expectedObj.fields !== undefined) {
      assertFieldsEqual(
        actualObj.fields as FieldDef[],
        expectedObj.fields as FieldDef[],
        ignoreFieldOids,
      );
    }
    return;
  }

  // Direct comparison
  assertValueEqual(actual, expected, floatTolerance);
}

/**
 * Assert that two rows are equivalent
 */
function assertRowsEqual(actual: unknown, expected: unknown, floatTolerance: number): void {
  if (Array.isArray(actual) && Array.isArray(expected)) {
    expect(actual.length).toBe(expected.length);
    for (let i = 0; i < actual.length; i++) {
      assertValueEqual(actual[i], expected[i], floatTolerance);
    }
    return;
  }

  if (
    actual !== null &&
    expected !== null &&
    typeof actual === "object" &&
    typeof expected === "object"
  ) {
    const actualObj = actual as Record<string, unknown>;
    const expectedObj = expected as Record<string, unknown>;
    const actualKeys = Object.keys(actualObj).sort();
    const expectedKeys = Object.keys(expectedObj).sort();
    expect(actualKeys).toEqual(expectedKeys);

    for (const key of actualKeys) {
      assertValueEqual(actualObj[key], expectedObj[key], floatTolerance);
    }
    return;
  }

  assertValueEqual(actual, expected, floatTolerance);
}

/**
 * Assert that two values are equivalent
 */
function assertValueEqual(actual: unknown, expected: unknown, floatTolerance: number): void {
  // Handle null/undefined
  if (actual === null || actual === undefined) {
    expect(actual).toBe(expected as null | undefined);
    return;
  }

  // Handle dates
  if (actual instanceof Date && expected instanceof Date) {
    expect(actual.getTime()).toBe(expected.getTime());
    return;
  }

  // Handle date strings
  if (
    typeof actual === "string" &&
    typeof expected === "string" &&
    isISODateString(actual) &&
    isISODateString(expected)
  ) {
    expect(new Date(actual).getTime()).toBe(new Date(expected).getTime());
    return;
  }

  // Handle buffers/Uint8Arrays
  if (
    (actual instanceof Buffer || actual instanceof Uint8Array) &&
    (expected instanceof Buffer || expected instanceof Uint8Array)
  ) {
    expect(Array.from(actual)).toEqual(Array.from(expected));
    return;
  }

  // Handle numbers with tolerance
  if (typeof actual === "number" && typeof expected === "number") {
    if (Number.isNaN(actual) && Number.isNaN(expected)) {
      return; // Both NaN is considered equal
    }
    if (!Number.isFinite(actual) || !Number.isFinite(expected)) {
      expect(actual).toBe(expected);
      return;
    }
    expect(Math.abs(actual - expected)).toBeLessThanOrEqual(floatTolerance);
    return;
  }

  // Handle bigint
  if (typeof actual === "bigint" && typeof expected === "bigint") {
    expect(actual).toBe(expected);
    return;
  }

  // Handle arrays
  if (Array.isArray(actual) && Array.isArray(expected)) {
    expect(actual.length).toBe(expected.length);
    for (let i = 0; i < actual.length; i++) {
      assertValueEqual(actual[i], expected[i], floatTolerance);
    }
    return;
  }

  // Handle objects
  if (
    actual !== null &&
    expected !== null &&
    typeof actual === "object" &&
    typeof expected === "object"
  ) {
    const actualObj = actual as Record<string, unknown>;
    const expectedObj = expected as Record<string, unknown>;
    const actualKeys = Object.keys(actualObj).sort();
    const expectedKeys = Object.keys(expectedObj).sort();
    expect(actualKeys).toEqual(expectedKeys);
    for (const key of actualKeys) {
      assertValueEqual(actualObj[key], expectedObj[key], floatTolerance);
    }
    return;
  }

  // Default comparison
  expect(actual).toBe(expected as typeof actual);
}

/**
 * Assert that field definitions match
 */
function assertFieldsEqual(actual: FieldDef[], expected: FieldDef[], ignoreOids: boolean): void {
  expect(actual.length).toBe(expected.length);

  for (let i = 0; i < actual.length; i++) {
    expect(actual[i].name).toBe(expected[i].name);

    if (!ignoreOids) {
      expect(actual[i].dataTypeID).toBe(expected[i].dataTypeID);
      if (expected[i].tableID !== undefined) {
        expect(actual[i].tableID).toBe(expected[i].tableID);
      }
    }
  }
}

/**
 * Row comparator for sorting
 */
function rowComparator(a: unknown, b: unknown): number {
  const aStr = JSON.stringify(a);
  const bStr = JSON.stringify(b);
  return aStr.localeCompare(bStr);
}

/**
 * Check if a string looks like an ISO date
 */
function isISODateString(str: string): boolean {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/.test(str);
}

/**
 * Database error structure
 */
interface DbError {
  code?: string;
  severity?: string;
  message?: string;
  constraint?: string;
  table?: string;
  column?: string;
}

/**
 * Assert that an error matches expected properties
 */
export function assertErrorMatches(
  error: unknown,
  expected: {
    code?: string;
    severity?: string;
    messageContains?: string;
    constraint?: string;
    table?: string;
    column?: string;
  },
): void {
  const err = error as DbError;
  if (expected.code) {
    expect(err.code as string).toBe(expected.code);
  }
  if (expected.severity) {
    expect(err.severity).toBe(expected.severity);
  }
  if (expected.messageContains) {
    expect(err.message).toContain(expected.messageContains);
  }
  if (expected.constraint) {
    expect(err.constraint).toBe(expected.constraint);
  }
  if (expected.table) {
    expect(err.table).toBe(expected.table);
  }
  if (expected.column) {
    expect(err.column).toBe(expected.column);
  }
}

/**
 * Assert that a query result has expected structure
 */
export function assertFullResultStructure(result: FullQueryResults<boolean>): void {
  expect(result).toHaveProperty("rows");
  expect(result).toHaveProperty("fields");
  expect(result).toHaveProperty("command");
  expect(result).toHaveProperty("rowCount");
  expect(result).toHaveProperty("rowAsArray");

  expect(Array.isArray(result.rows)).toBe(true);
  expect(Array.isArray(result.fields)).toBe(true);
  expect(typeof result.command).toBe("string");
  expect(typeof result.rowCount).toBe("number");
  expect(typeof result.rowAsArray).toBe("boolean");
}

/**
 * Assert that a value is a valid PostgreSQL type representation
 */
export function assertValidPgType(value: unknown, pgType: string): void {
  switch (pgType) {
    case "integer":
    case "smallint":
    case "bigint":
      expect(
        typeof value === "number" || typeof value === "bigint" || typeof value === "string",
      ).toBe(true);
      break;
    case "real":
    case "double precision":
    case "numeric":
      expect(typeof value === "number" || typeof value === "string").toBe(true);
      break;
    case "boolean":
      expect(typeof value === "boolean").toBe(true);
      break;
    case "text":
    case "varchar":
    case "char":
      expect(typeof value === "string").toBe(true);
      break;
    case "bytea":
      expect(
        value instanceof Buffer || value instanceof Uint8Array || typeof value === "string",
      ).toBe(true);
      break;
    case "json":
    case "jsonb":
      expect(value !== undefined).toBe(true);
      break;
    case "uuid":
      expect(typeof value === "string").toBe(true);
      expect(
        /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value as string),
      ).toBe(true);
      break;
    case "timestamp":
    case "timestamptz":
    case "date":
      expect(value instanceof Date || typeof value === "string" || typeof value === "number").toBe(
        true,
      );
      break;
    default:
      // For unknown types, just check it's not undefined
      expect(value !== undefined).toBe(true);
  }
}

/**
 * Assert arrays are deeply equal regardless of element order
 */
export function assertArraysEqualUnordered(actual: unknown[], expected: unknown[]): void {
  expect(actual.length).toBe(expected.length);

  const sortedActual = [...actual].sort((a, b) =>
    JSON.stringify(a).localeCompare(JSON.stringify(b)),
  );
  const sortedExpected = [...expected].sort((a, b) =>
    JSON.stringify(a).localeCompare(JSON.stringify(b)),
  );

  expect(sortedActual).toEqual(sortedExpected);
}

/**
 * Assert that a result contains specific columns
 */
export function assertHasColumns(result: FullQueryResults<boolean>, columns: string[]): void {
  const fieldNames = result.fields.map((f) => f.name);
  for (const col of columns) {
    expect(fieldNames).toContain(col);
  }
}

/**
 * Assert that execution time is within acceptable bounds
 */
export function assertExecutionTime(startTime: number, maxMs: number, _label = "Query"): void {
  const elapsed = Date.now() - startTime;
  expect(elapsed).toBeLessThanOrEqual(maxMs);
}
