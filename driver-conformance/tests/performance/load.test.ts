/**
 * Load Tests (15 tests)
 *
 * Tests for behavior under various load conditions
 */

import { neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Load Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("load_test");

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

  // ==================== Sustained Load ====================
  describe("Sustained Load", () => {
    test("sustained 10 queries per iteration for 10 iterations", async () => {
      let successCount = 0;
      let errorCount = 0;

      for (let i = 0; i < 10; i++) {
        const queries = Array.from({ length: 10 }, (_, j) =>
          sql`SELECT ${i * 10 + j} as n`.catch(() => {
            errorCount++;
            return null;
          }),
        );

        const results = await Promise.all(queries);
        successCount += results.filter((r) => r !== null).length;
      }

      expect(successCount).toBe(100);
      expect(errorCount).toBe(0);
    });

    test("sustained queries over time", async () => {
      const results: boolean[] = [];
      const duration = 2000; // 2 seconds
      const start = Date.now();

      while (Date.now() - start < duration) {
        try {
          await sql`SELECT 1`;
          results.push(true);
        } catch {
          results.push(false);
        }
      }

      const successRate = results.filter((r) => r).length / results.length;
      expect(successRate).toBeGreaterThan(0.9);
    });
  });

  // ==================== Burst Load ====================
  describe("Burst Load", () => {
    test("burst of 50 concurrent queries", async () => {
      const queries = Array.from({ length: 50 }, (_, i) => sql`SELECT ${i} as n`);

      const results = await Promise.all(queries);
      expect(results.length).toBe(50);
    });

    test("burst of 100 concurrent queries", async () => {
      const queries = Array.from({ length: 100 }, (_, i) => sql`SELECT ${i} as n`);

      const results = await Promise.all(queries);
      expect(results.length).toBe(100);
    });

    test("multiple bursts", async () => {
      for (let burst = 0; burst < 5; burst++) {
        const queries = Array.from({ length: 20 }, (_, i) => sql`SELECT ${burst * 20 + i} as n`);
        const results = await Promise.all(queries);
        expect(results.length).toBe(20);
      }
    });
  });

  // ==================== Ramp Up/Down ====================
  describe("Ramp Up/Down", () => {
    test("ramp up load", async () => {
      const concurrencyLevels = [1, 5, 10, 20, 30];

      for (const level of concurrencyLevels) {
        const queries = Array.from({ length: level }, (_, i) => sql`SELECT ${i} as n`);
        const results = await Promise.all(queries);
        expect(results.length).toBe(level);
      }
    });

    test("ramp down load", async () => {
      const concurrencyLevels = [30, 20, 10, 5, 1];

      for (const level of concurrencyLevels) {
        const queries = Array.from({ length: level }, (_, i) => sql`SELECT ${i} as n`);
        const results = await Promise.all(queries);
        expect(results.length).toBe(level);
      }
    });
  });

  // ==================== Connection Pool Sizing ====================
  describe("Connection Pool Sizing", () => {
    test("pool with max=2 handles concurrent requests", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 2 });

      const queries = Array.from({ length: 10 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(10);

      await pool.end();
    });

    test("pool with max=5 handles concurrent requests", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 20 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(20);

      await pool.end();
    });

    test("pool with max=10 handles concurrent requests", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 10 });

      const queries = Array.from({ length: 50 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(50);

      await pool.end();
    });
  });

  // ==================== Error Rate Under Load ====================
  describe("Error Rate Under Load", () => {
    test("error rate remains low under moderate load", async () => {
      let errors = 0;
      const totalQueries = 50;

      const queries = Array.from({ length: totalQueries }, () =>
        sql`SELECT 1`.catch(() => {
          errors++;
          return null;
        }),
      );

      await Promise.all(queries);
      const errorRate = errors / totalQueries;

      expect(errorRate).toBeLessThan(0.1); // Less than 10% error rate
    });

    test("system recovers after errors", async () => {
      // Generate some errors
      try {
        await sql`SELECT * FROM nonexistent_load_table`;
      } catch {
        // Expected
      }

      // System should still work
      const results = await Promise.all([
        sql`SELECT 1 as n`,
        sql`SELECT 2 as n`,
        sql`SELECT 3 as n`,
      ]);

      expect(results.length).toBe(3);
    });
  });

  // ==================== Recovery Testing ====================
  describe("Recovery Testing", () => {
    test("recovers after failed query", async () => {
      try {
        await sql`INVALID SYNTAX`;
      } catch {
        // Expected
      }

      const result = await sql`SELECT 'recovered' as status`;
      expect(asRows(result)[0].status).toBe("recovered");
    });

    test("handles mixed success/failure gracefully", async () => {
      const queries = [
        sql`SELECT 1 as n`,
        sql`SELECT * FROM nonexistent_recovery_table`.catch(() => ({
          error: true,
        })),
        sql`SELECT 2 as n`,
        sql`INVALID`.catch(() => ({ error: true })),
        sql`SELECT 3 as n`,
      ];

      const results = await Promise.all(queries);
      expect(results.length).toBe(5);

      // Valid queries should succeed
      expect(asRows(results[0])[0].n).toBe(1);
      expect(asRows(results[2])[0].n).toBe(2);
      expect(asRows(results[4])[0].n).toBe(3);
    });
  });

  // ==================== Graceful Degradation ====================
  describe("Graceful Degradation", () => {
    test("handles slow queries mixed with fast queries", async () => {
      const queries = [
        sql`SELECT 1 as n`, // Fast
        sql`SELECT pg_sleep(0.1), 2 as n`, // Slow
        sql`SELECT 3 as n`, // Fast
      ];

      const results = await Promise.all(queries);
      expect(results.length).toBe(3);
    });

    test("pool handles connection wait gracefully", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 1 });

      // Queue up multiple queries with single connection
      const queries = Array.from({ length: 5 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(5);

      await pool.end();
    });
  });
});
