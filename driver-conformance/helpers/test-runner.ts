/**
 * Test utilities for consistent test execution
 *
 * Provides helper functions for running database tests.
 */

import type { NeonDbError } from "@neondatabase/serverless";
import { expect } from "vitest";
import { getConnection } from "./connection";

export interface QueryTestOptions {
  arrayMode?: boolean;
  fullResults?: boolean;
  isolationLevel?: "ReadUncommitted" | "ReadCommitted" | "RepeatableRead" | "Serializable";
  readOnly?: boolean;
  deferrable?: boolean;
}

/**
 * Execute a query and return the result
 */
export async function executeQuery(
  query: string,
  params: unknown[] = [],
  options: QueryTestOptions = {},
): Promise<unknown> {
  const sql = getConnection({
    arrayMode: options.arrayMode ?? false,
    fullResults: options.fullResults ?? true,
  });

  return sql.query(query, params, {
    arrayMode: options.arrayMode,
    fullResults: options.fullResults ?? true,
  });
}

/**
 * Execute a query expecting success
 */
export async function expectQuerySuccess(
  query: string,
  params: unknown[] = [],
  options: QueryTestOptions = {},
): Promise<unknown> {
  const result = await executeQuery(query, params, options);
  expect(result).toBeDefined();
  return result;
}

/**
 * Execute a query expecting a specific error code
 */
export async function expectQueryError(
  query: string,
  params: unknown[] = [],
  expectedCode?: string,
): Promise<unknown> {
  const sql = getConnection();
  try {
    await sql.query(query, params);
    throw new Error("Expected query to fail but it succeeded");
  } catch (e: unknown) {
    const error = e as Error;
    if (error.message === "Expected query to fail but it succeeded") {
      throw e;
    }
    if (expectedCode) {
      const dbError = e as NeonDbError;
      if (dbError.code !== expectedCode) {
        throw new Error(
          `Expected error code "${expectedCode}" but got "${dbError.code}": ${dbError.message}`,
        );
      }
    }
    return e;
  }
}
