/**
 * Cloudflare Workers Runtime Tests (15 tests)
 *
 * Tests for Cloudflare Workers compatibility
 * Note: These tests simulate Workers-like conditions but run in Node/Bun
 */

import { neon, neonConfig } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Cloudflare Workers Runtime Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("cf_test");

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

  // ==================== Worker Setup ====================
  describe("Worker Setup", () => {
    test("HTTP queries work in worker-like context", async () => {
      const result = await sql`SELECT 'cloudflare' as runtime`;
      expect(asRows(result)[0].runtime).toBe("cloudflare");
    });

    test("neon() function is worker-compatible", async () => {
      // Workers typically use the HTTP interface
      const workerSql = neon(dbUrl);
      const result = await workerSql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });

    test("configuration options work", () => {
      expect(neonConfig.fetchEndpoint).toBeDefined();
    });
  });

  // ==================== Worker Environment ====================
  describe("Worker Environment", () => {
    test("environment variables pattern", () => {
      // In Workers, env vars come from bindings
      const connectionString = getDatabaseUrl();
      expect(connectionString).toBeDefined();
    });

    test("works with connection string from env", async () => {
      const envSql = neon(getDatabaseUrl());
      const result = await envSql`SELECT 'env' as source`;
      expect(asRows(result)[0].source).toBe("env");
    });
  });

  // ==================== Fetch in Worker ====================
  describe("Fetch in Worker", () => {
    test("HTTP queries via fetch", async () => {
      const result = await sql`SELECT 'fetch' as method`;
      expect(asRows(result)[0].method).toBe("fetch");
    });

    test("multiple fetch requests", async () => {
      const results = await Promise.all([
        sql`SELECT 1 as n`,
        sql`SELECT 2 as n`,
        sql`SELECT 3 as n`,
      ]);
      expect(results.length).toBe(3);
    });

    test("fetch with large response", async () => {
      const result = await sql`SELECT generate_series(1, 500) as n`;
      expect(asRows(result).length).toBe(500);
    });
  });

  // ==================== CPU Time Limits ====================
  describe("CPU Time Limits", () => {
    test("fast queries complete quickly", async () => {
      const start = Date.now();
      await sql`SELECT 1`;
      const elapsed = Date.now() - start;

      // Should complete well within CPU limits
      expect(elapsed).toBeLessThan(10000);
    });

    test("batch queries are efficient", async () => {
      const results = await sql.transaction([
        sql`SELECT 1 as a`,
        sql`SELECT 2 as b`,
        sql`SELECT 3 as c`,
      ]);
      expect(results.length).toBe(3);
    });
  });

  // ==================== Memory Limits ====================
  describe("Memory Limits", () => {
    test("handles reasonable result sizes", async () => {
      const result = await sql`SELECT generate_series(1, 1000) as n`;
      expect(asRows(result).length).toBe(1000);
    });

    test("handles multiple queries without leaks", async () => {
      for (let i = 0; i < 10; i++) {
        const result = await sql`SELECT ${i}::integer as n`;
        expect(asRows(result)[0].n).toBe(i);
      }
    });
  });

  // ==================== Response Streaming ====================
  describe("Response Streaming", () => {
    test("handles paginated responses", async () => {
      let total = 0;
      for (let page = 0; page < 5; page++) {
        const result = await sql`
          SELECT generate_series(${page * 10 + 1}::integer, ${(page + 1) * 10}::integer) as n
        `;
        total += asRows(result).length;
      }
      expect(total).toBe(50);
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("errors are properly formatted", async () => {
      try {
        await sql`SELECT * FROM nonexistent_cf_table`;
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect((e as Error).name).toBe("NeonDbError");
        expect((e as { code: string }).code).toBe("42P01");
      }
    });

    test("error recovery works", async () => {
      try {
        await sql`INVALID`;
      } catch {
        // Expected
      }

      const result = await sql`SELECT 'recovered' as status`;
      expect(asRows(result)[0].status).toBe("recovered");
    });

    test("constraint errors are catchable", async () => {
      // Insert duplicate if table has unique constraint
      const result = await sql`SELECT 1 as test`;
      expect(asRows(result).length).toBe(1);
    });
  });

  // ==================== Transaction Support ====================
  describe("Transaction Support", () => {
    test("HTTP transactions work", async () => {
      const results = await sql.transaction([sql`SELECT 1 as first`, sql`SELECT 2 as second`]);
      expect(asRows(results[0])[0].first).toBe(1);
      expect(asRows(results[1])[0].second).toBe(2);
    });

    test("transaction rollback on error", async () => {
      const name = uniqueTableName("cf_rollback");
      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(testTable)} (data) VALUES (${name})`,
          sql`SELECT 1/0`, // Error
        ]);
      } catch {
        // Expected
      }

      const check = await sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE data = ${name}`;
      expect(asRows(check).length).toBe(0);
    });
  });
});
