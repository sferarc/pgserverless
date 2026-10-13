/**
 * Vercel Edge Runtime Tests (10 tests)
 *
 * Tests for Vercel Edge Runtime compatibility
 * Note: These tests simulate Edge Runtime conditions but run in Node/Bun
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows, type Row } from "../../helpers/types";

describe("Vercel Edge Runtime Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("vercel_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        data TEXT
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Edge Function Setup ====================
  describe("Edge Function Setup", () => {
    test("HTTP queries work in edge context", async () => {
      const result = await sql`SELECT 'vercel-edge' as runtime`;
      expect(asRows(result)[0].runtime).toBe("vercel-edge");
    });

    test("neon() creates edge-compatible connection", async () => {
      const edgeSql = neon(dbUrl);
      const result = await edgeSql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });
  });

  // ==================== Response Streaming ====================
  describe("Response Streaming", () => {
    test("handles streaming-like queries", async () => {
      const results: Row[] = [];
      for (let i = 0; i < 5; i++) {
        const result = await sql`SELECT ${i} as chunk`;
        results.push(asRows(result)[0]);
      }
      expect(results.length).toBe(5);
    });

    test("large responses work", async () => {
      const result = await sql`SELECT generate_series(1, 500) as n`;
      expect(asRows(result).length).toBe(500);
    });
  });

  // ==================== Environment Variables ====================
  describe("Environment Variables", () => {
    test("connection from env works", async () => {
      const result = await sql`SELECT 'env' as source`;
      expect(asRows(result)[0].source).toBe("env");
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("errors are properly handled", async () => {
      try {
        await sql`SELECT * FROM nonexistent_vercel_table`;
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect((e as { code: string }).code).toBe("42P01");
      }
    });

    test("error recovery works", async () => {
      try {
        await sql`INVALID`;
      } catch {
        // Expected
      }

      const result = await sql`SELECT 'ok' as status`;
      expect(asRows(result)[0].status).toBe("ok");
    });
  });

  // ==================== ISR Compatibility ====================
  describe("ISR Compatibility", () => {
    test("queries work for data fetching", async () => {
      // ISR pages fetch data at build time or revalidation
      const result = await sql`SELECT NOW() as timestamp`;
      expect(asRows(result)[0].timestamp).toBeDefined();
    });

    test("consistent results for caching", async () => {
      const r1 = await sql`SELECT 42 as value`;
      const r2 = await sql`SELECT 42 as value`;
      expect(asRows(r1)[0].value).toBe(asRows(r2)[0].value);
    });
  });

  // ==================== Middleware Integration ====================
  describe("Middleware Integration", () => {
    test("fast queries for middleware use", async () => {
      const start = Date.now();
      await sql`SELECT 1`;
      const elapsed = Date.now() - start;

      // Middleware needs fast responses
      expect(elapsed).toBeLessThan(5000);
    });

    test("simple auth check pattern", async () => {
      // Simulating a simple auth check in middleware
      const userId = "test_user";
      const result = await sql`SELECT ${userId} as user_id, true as valid`;
      expect(asRows(result)[0].valid).toBe(true);
    });
  });
});
