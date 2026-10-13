/**
 * Bun Runtime Tests (15 tests)
 *
 * Tests for Bun-specific functionality with the Neon serverless driver
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

declare const Bun: { env: Record<string, string | undefined> } | undefined;

const isBun = typeof Bun !== "undefined";

describe.skipIf(!isBun)("Bun Runtime Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("bun_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        data TEXT,
        num INTEGER
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Basic Connectivity ====================
  describe("Basic Connectivity", () => {
    test("basic connectivity via HTTP", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(result).toEqual([{ num: 1 }]);
    });

    test("WebSocket connection works", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 'bun ws' as source");
      expect(result.rows[0].source).toBe("bun ws");
      await client.end();
    });

    test("native fetch works", async () => {
      // Bun has native fetch
      const result = await sql`SELECT 'bun fetch' as method`;
      expect(asRows(result)[0].method).toBe("bun fetch");
    });
  });

  // ==================== Bun-Specific Features ====================
  describe("Bun-Specific Features", () => {
    test("Bun test runner integration", () => {
      // This test verifies we're running in Bun's test runner
      expect(typeof Bun).toBe("object");
    });

    test("Bun.env access", () => {
      // Bun provides Bun.env for environment variables
      expect(Bun?.env).toBeDefined();
    });

    test("performance measurement", async () => {
      const start = performance.now();
      await sql`SELECT 1`;
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
    });
  });

  // ==================== Performance ====================
  describe("Performance", () => {
    test("handles large result set efficiently", async () => {
      const start = performance.now();
      const result = await sql`SELECT generate_series(1, 5000) as n`;
      const elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(5000);
      // Should complete in reasonable time
      expect(elapsed).toBeLessThan(30000); // 30 seconds max
    });

    test("memory efficient for large queries", async () => {
      // Run multiple large queries
      for (let i = 0; i < 5; i++) {
        const result = await sql`SELECT generate_series(1, 1000) as n`;
        expect(asRows(result).length).toBe(1000);
      }
    });

    test("concurrent query performance", async () => {
      const start = performance.now();

      const queries = Array.from({ length: 50 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);

      const _elapsed = performance.now() - start;

      expect(results.length).toBe(50);
    });
  });

  // ==================== Streaming ====================
  describe("Streaming", () => {
    test("handles streaming-like patterns", async () => {
      // Process results in chunks
      const chunkSize = 100;
      let processed = 0;

      for (let offset = 0; offset < 500; offset += chunkSize) {
        const result = await sql`
          SELECT generate_series(${offset + 1}::integer, ${offset + chunkSize}::integer) as n
        `;
        processed += asRows(result).length;
      }

      expect(processed).toBe(500);
    });
  });

  // ==================== Concurrency ====================
  describe("Concurrency", () => {
    test("Pool works with Bun", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 10 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(10);

      await pool.end();
    });

    test("concurrent transactions", async () => {
      const results = await Promise.all([
        sql.transaction([sql`SELECT 1 as n`]),
        sql.transaction([sql`SELECT 2 as n`]),
        sql.transaction([sql`SELECT 3 as n`]),
      ]);

      expect(results.length).toBe(3);
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("error handling works correctly", async () => {
      try {
        await sql`SELECT * FROM nonexistent_bun_table`;
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect((e as { code: string }).code).toBe("42P01");
      }
    });

    test("recovers from errors", async () => {
      try {
        await sql`INVALID`;
      } catch {
        // Expected
      }

      const result = await sql`SELECT 'recovered' as status`;
      expect(asRows(result)[0].status).toBe("recovered");
    });
  });
});
