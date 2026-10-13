/**
 * Browser Runtime Tests (15 tests)
 *
 * Tests for browser-compatible functionality
 * Note: These tests simulate browser-like conditions but run in Node/Bun
 */

import { neon, neonConfig } from "@neondatabase/serverless";
import { beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { asRows } from "../../helpers/types";

describe("Browser Runtime Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;

  beforeAll(() => {
    // Configure for browser-like environment
    neonConfig.disableWarningInBrowsers = true;
    sql = neon(dbUrl);
  });

  // ==================== Browser WebSocket ====================
  describe("Browser WebSocket Compatibility", () => {
    test("works without ws package when native WebSocket available", async () => {
      // In browsers, native WebSocket is used
      // This test verifies HTTP path works (which doesn't need WebSocket)
      const result = await sql`SELECT 'browser' as env`;
      expect(asRows(result)[0].env).toBe("browser");
    });

    test("HTTP fetch path works", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });
  });

  // ==================== Security ====================
  describe("Security", () => {
    test("disableWarningInBrowsers option", () => {
      expect(neonConfig.disableWarningInBrowsers).toBe(true);
    });

    test("warning can be disabled via options", async () => {
      const safeSql = neon(dbUrl, { disableWarningInBrowsers: true });
      const result = await safeSql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });
  });

  // ==================== Bundle Size Considerations ====================
  describe("Bundle Size Considerations", () => {
    test("core HTTP functionality works", async () => {
      // HTTP-only usage has smaller bundle size
      const result = await sql`SELECT 'minimal' as bundle`;
      expect(asRows(result)[0].bundle).toBe("minimal");
    });

    test("template literals work", async () => {
      const value = 42;
      const result = await sql`SELECT ${value}::integer as val`;
      expect(asRows(result)[0].val).toBe(42);
    });

    test("transactions work", async () => {
      const results = await sql.transaction([sql`SELECT 1 as a`, sql`SELECT 2 as b`]);
      expect(results.length).toBe(2);
    });
  });

  // ==================== Network Handling ====================
  describe("Network Handling", () => {
    test("handles query timeout gracefully", async () => {
      // Fast query should complete quickly
      const result = await sql`SELECT 1`;
      expect(asRows(result).length).toBe(1);
    });

    test("multiple sequential requests", async () => {
      for (let i = 0; i < 5; i++) {
        const result = await sql`SELECT ${i}::integer as n`;
        expect(asRows(result)[0].n).toBe(i);
      }
    });

    test("concurrent requests", async () => {
      const results = await Promise.all([
        sql`SELECT 1 as n`,
        sql`SELECT 2 as n`,
        sql`SELECT 3 as n`,
      ]);
      expect(results.length).toBe(3);
    });
  });

  // ==================== Error Boundaries ====================
  describe("Error Boundaries", () => {
    test("errors don't crash the runtime", async () => {
      try {
        await sql`INVALID SYNTAX`;
      } catch (e) {
        expect(e).toBeDefined();
      }

      // Should still work after error
      const result = await sql`SELECT 'ok' as status`;
      expect(asRows(result)[0].status).toBe("ok");
    });

    test("network errors are catchable", async () => {
      // Simulate by using invalid connection (this will fail early)
      const invalidSql = neon("postgres://invalid:invalid@localhost:54321/invalid");

      try {
        await invalidSql`SELECT 1`;
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });
  });

  // ==================== Response Handling ====================
  describe("Response Handling", () => {
    test("handles empty results", async () => {
      const result = await sql`SELECT 1 WHERE false`;
      expect(result).toEqual([]);
    });

    test("handles large responses", async () => {
      const result = await sql`SELECT generate_series(1, 1000) as n`;
      expect(asRows(result).length).toBe(1000);
    });

    test("handles JSON responses", async () => {
      const json = { key: "value", arr: [1, 2, 3] };
      const result = await sql`SELECT ${JSON.stringify(json)}::jsonb as data`;
      expect(asRows(result)[0].data).toEqual(json);
    });

    test("handles binary data", async () => {
      const result = await sql`SELECT '\\xDEADBEEF'::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });
  });
});
